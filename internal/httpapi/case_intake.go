package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/store"
)

const (
	intakeRequestBodyLimit = 33 << 20
	intakeMaxFileBytes     = 32 << 20
)

type intakeUpload struct {
	title        string
	contractType string
	businessLine string
	scenario     string
	filename     string
	mediaType    string
	body         []byte
}

// createIntake implements POST /api/intakes. Core creates the operation and
// case in one short transaction, then delegates the primary-contract upload to
// Document before conditionally freezing the public receipt.
func (h *handler) createIntake(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	upload, key, err := parseIntakeRequest(w, r)
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	operation, err := h.store.BeginCaseIntake(r.Context(), user, store.CaseIntakeInput{
		Title: upload.title, ContractType: upload.contractType, BusinessLine: upload.businessLine,
		Scenario: upload.scenario, Filename: upload.filename, MediaType: upload.mediaType,
		Body: upload.body, IdempotencyKey: key,
	})
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	if operation.Status == "completed" {
		writeIntakeOperation(w, operation, http.StatusCreated)
		return
	}
	if h.intakeUploader == nil || operation.UploadKey == "" {
		writeIntakeOperation(w, operation, http.StatusAccepted)
		return
	}
	uploadRequest := store.PrimaryContractUploadRequest{
		ActorID: user.ID, TenantID: accounts.Tenant(user), OrgID: user.OrgID, User: user,
		Filename: upload.filename, MediaType: upload.mediaType, Body: upload.body,
		IdempotencyKey: operation.UploadKey,
	}
	if _, direct := h.intakeUploader.(store.DirectCaseIntakeUploader); !direct {
		ticket, ticketErr := h.store.CreateDocumentIntakeTicket(r.Context(), user, audienceCookieValue(r, "client"),
			operation.OperationID, operation.CaseID, operation.UploadKey, upload.body)
		if ticketErr != nil {
			writeIntakeError(w, ticketErr)
			return
		}
		uploadRequest.AuthorityTicket = ticket
	}
	result, uploadErr := h.intakeUploader.UploadPrimaryContract(r.Context(), operation.CaseID, uploadRequest)
	if uploadErr == nil {
		completed, completeErr := h.store.CompleteCaseIntake(r.Context(), user, operation.OperationID, result)
		if completeErr == nil && completed.Status == "completed" {
			writeIntakeOperation(w, completed, http.StatusCreated)
			return
		}
	}
	// A concurrent request or a lost upstream response may already have
	// completed this exact operation. Never report a stable pending state when
	// the frozen receipt is available; an unreadable operation is an integrity
	// or authorization failure, not a retryable upload.
	latest, latestErr := h.store.GetCaseIntake(r.Context(), user, operation.OperationID)
	if latestErr != nil {
		writeIntakeError(w, latestErr)
		return
	}
	if latest.Status == "completed" {
		writeIntakeOperation(w, latest, http.StatusCreated)
		return
	}
	writeIntakeOperation(w, latest, http.StatusAccepted)
}

// getIntake implements GET /api/intakes/{operationID}.
func (h *handler) getIntake(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	operation, err := h.store.GetCaseIntake(r.Context(), user, r.PathValue("operationID"))
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	writeIntakeOperation(w, operation, http.StatusOK)
}

// getIntakeByKey implements GET /api/intakes?key=... and recovers the current
// trusted tenant+actor operation without re-uploading file bytes.
func (h *handler) getIntakeByKey(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeIntakeError(w, fault.Invalid("查询参数格式不正确"))
		return
	}
	values := query["key"]
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		writeIntakeError(w, fault.Invalid("缺少建案幂等键"))
		return
	}
	operation, err := h.store.GetCaseIntakeByKey(r.Context(), user, values[0])
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	writeIntakeOperation(w, operation, http.StatusOK)
}

// listPendingIntakes resolves personal unfinished operations from PostgreSQL.
func (h *handler) listPendingIntakes(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["limit"]) > 1 || len(query["cursor"]) > 1 {
		writeIntakeError(w, fault.Invalid("待办查询参数格式不正确"))
		return
	}
	limit := 10
	if values := query["limit"]; len(values) == 1 {
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > 50 {
			writeIntakeError(w, fault.Invalid("待办每页数量必须为1至50"))
			return
		}
	}
	page, err := h.store.ListPendingCaseIntakes(r.Context(), user, limit, query.Get("cursor"))
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// getIntakeContinuation exposes replay identity only to the original actor.
func (h *handler) getIntakeContinuation(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	continuation, err := h.store.GetCaseIntakeContinuation(r.Context(), user, r.PathValue("operationID"))
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, continuation)
}

// archiveIntake appends a personal archive fact without changing the case.
func (h *handler) archiveIntake(w http.ResponseWriter, r *http.Request) {
	user, ok := h.actor(w, r)
	if !ok {
		return
	}
	var input struct{}
	if err := readJSON(w, r, &input); err != nil {
		writeIntakeError(w, err)
		return
	}
	archived, err := h.store.ArchiveCaseIntake(r.Context(), user, r.PathValue("operationID"))
	if err != nil {
		writeIntakeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, archived)
}

func parseIntakeRequest(w http.ResponseWriter, r *http.Request) (intakeUpload, string, error) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		return intakeUpload{}, "", fault.Invalid("Idempotency-Key必须是唯一的请求头")
	}
	key := keys[0]
	if len(key) < 1 || len(key) > 200 {
		return intakeUpload{}, "", fault.Invalid("Idempotency-Key长度必须是1到200字符")
	}
	for _, char := range key {
		if char < 0x21 || char > 0x7e {
			return intakeUpload{}, "", fault.Invalid("Idempotency-Key只能包含可见ASCII字符")
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, intakeRequestBodyLimit)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "too large") || errors.Is(err, io.ErrUnexpectedEOF) {
			return intakeUpload{}, "", fault.TooLarge("建案上传请求超过 33 MiB")
		}
		return intakeUpload{}, "", fault.Invalid("建案上传格式不正确")
	}
	defer r.MultipartForm.RemoveAll()

	for name, fixed := range map[string]string{"intent": "add", "role": "draft_contract", "purpose": "primary_contract"} {
		value, err := intakeFormValue(r, name)
		if err != nil {
			return intakeUpload{}, "", err
		}
		if value != "" && value != fixed {
			return intakeUpload{}, "", fault.Invalid("建案上传的意图、角色与用途固定为主合同新增")
		}
	}
	if len(r.MultipartForm.File) != 1 {
		return intakeUpload{}, "", fault.Invalid("建案上传需要且只能包含一个文件")
	}
	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		return intakeUpload{}, "", fault.Invalid("缺少文件")
	}
	title, err := intakeFormValue(r, "title")
	if err != nil {
		return intakeUpload{}, "", err
	}
	contractType, err := intakeFormValue(r, "contract_type")
	if err != nil {
		return intakeUpload{}, "", err
	}
	businessLine, err := intakeFormValue(r, "business_line")
	if err != nil {
		return intakeUpload{}, "", err
	}
	scenario, err := intakeFormValue(r, "scenario")
	if err != nil {
		return intakeUpload{}, "", err
	}
	file, err := files[0].Open()
	if err != nil {
		return intakeUpload{}, "", fault.Invalid("文件无法读取")
	}
	defer file.Close()
	if files[0].Size > intakeMaxFileBytes {
		return intakeUpload{}, "", fault.TooLarge("文件超过 32 MiB")
	}
	// ParseMultipartForm measured the complete part; allocate its exact size
	// once instead of growing and copying a large file buffer repeatedly.
	body := make([]byte, int(files[0].Size))
	if _, err := io.ReadFull(file, body); err != nil {
		return intakeUpload{}, "", fault.Invalid("文件无法读取")
	}
	filename := canonicalIntakeFilename(files[0].Filename)
	for _, char := range filename {
		if char < 0x20 || char == 0x7f {
			return intakeUpload{}, "", fault.Invalid("文件名包含无效字符")
		}
	}
	return intakeUpload{
		title: title, contractType: contractType, businessLine: businessLine, scenario: scenario,
		filename: filename, mediaType: canonicalIntakeMedia(http.DetectContentType(body)), body: body,
	}, key, nil
}

func canonicalIntakeFilename(name string) string {
	name = strings.TrimSpace(filepathBase(strings.ReplaceAll(name, "\\", "/")))
	if name == "" || name == "." || name == ".." {
		return "upload.txt"
	}
	return name
}

func filepathBase(name string) string {
	if index := strings.LastIndex(name, "/"); index >= 0 {
		return name[index+1:]
	}
	return name
}

func canonicalIntakeMedia(media string) string {
	media = strings.TrimSpace(media)
	if index := strings.Index(media, ";"); index >= 0 {
		media = strings.TrimSpace(media[:index])
	}
	if media == "" {
		return "application/octet-stream"
	}
	return media
}

func intakeFormValue(r *http.Request, name string) (string, error) {
	values := r.MultipartForm.Value[name]
	if len(values) == 0 {
		return "", nil
	}
	for _, value := range values[1:] {
		if value != values[0] {
			return "", fault.Invalid("建案上传包含冲突的字段值")
		}
	}
	return values[0], nil
}

func writeIntakeOperation(w http.ResponseWriter, operation store.CaseIntakeOperation, status int) {
	if operation.Status == "completed" {
		if len(operation.ReceiptBody) == 0 || operation.Digest == "" {
			writeIntakeError(w, errors.New("completed intake operation lacks a frozen response"))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write(operation.ReceiptBody)
		return
	}
	writeIntakeJSON(w, status, operation.PendingBody())
}

func writeIntakeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeIntakeError(w http.ResponseWriter, err error) {
	var failure *fault.Error
	if errors.As(err, &failure) {
		body, _ := json.Marshal(map[string]any{"error": map[string]any{
			"code": failure.Code, "message": failure.Message, "retryable": failure.Retryable,
		}})
		writeIntakeJSON(w, failure.Status, body)
		return
	}
	log.Printf("intake request failed: %v", err)
	body, _ := json.Marshal(map[string]any{"error": map[string]any{
		"code": "internal", "message": "建案服务暂时不能完成这个操作", "retryable": true,
	}})
	writeIntakeJSON(w, http.StatusInternalServerError, body)
}
