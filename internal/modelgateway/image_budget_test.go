package modelgateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"testing"

	"contractaudit/internal/model"
)

func testImageBudget(t *testing.T) []byte {
	t.Helper()
	if path := os.Getenv("CAW_OCR_BUDGET_IMAGE_FILE"); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("cannot read private image fixture")
		}
		if len(body) <= 200_000 {
			t.Fatal("private OCR image fixture must exceed 200KB")
		}
		return body
	}
	picture := image.NewRGBA(image.Rect(0, 0, 512, 384))
	random := rand.New(rand.NewPCG(20261101, 1))
	for y := range 384 {
		for x := range 512 {
			picture.SetRGBA(x, y, color.RGBA{R: byte(random.IntN(256)), G: byte(random.IntN(256)), B: byte(random.IntN(256)), A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	if data.Len() <= 200_000 {
		t.Fatal("generated OCR image did not exercise the byte boundary")
	}
	return data.Bytes()
}

func testOCRPayload(t *testing.T, modelID, media, text string, imageBytes []byte, count int) []byte {
	t.Helper()
	picture := imageOCRPart{Type: "image_url"}
	picture.ImageURL.URL = "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(imageBytes)
	parts := []any{imageOCRText{Text: text, Type: "text"}}
	for range count {
		parts = append(parts, picture)
	}
	payload := struct {
		Model       string  `json:"model"`
		Temperature float64 `json:"temperature"`
		MaxTokens   int     `json:"max_tokens"`
		Messages    []struct {
			Role    string `json:"role"`
			Content []any  `json:"content"`
		} `json:"messages"`
	}{Model: modelID, Temperature: 0, MaxTokens: 8192}
	payload.Messages = append(payload.Messages, struct {
		Role    string `json:"role"`
		Content []any  `json:"content"`
	}{Role: "user", Content: parts})
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestImageTokenBudgetConfigurationRequiresCompleteBinding(t *testing.T) {
	for _, tc := range []struct {
		name, provider, modelID, upper string
		valid                          bool
	}{
		{"disabled", "", "", "", true},
		{"bound", "http://127.0.0.1:3425/v1", "panda-local/cq1/qwen3.6", "128000", true},
		{"missing provider", "", "cq1/qwen3.6", "128000", false},
		{"missing model", "http://127.0.0.1:3425/v1", "", "128000", false},
		{"missing bound", "http://127.0.0.1:3425/v1", "cq1/qwen3.6", "", false},
		{"zero", "http://127.0.0.1:3425/v1", "cq1/qwen3.6", "0", false},
		{"overflow", "http://127.0.0.1:3425/v1", "cq1/qwen3.6", "9223372036854775807", false},
		{"invalid provider", "http://user:password@example.test/v1", "cq1/qwen3.6", "128000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MODEL_OCR_IMAGE_PROVIDER_URL", tc.provider)
			t.Setenv("MODEL_OCR_IMAGE_MODEL", tc.modelID)
			t.Setenv("MODEL_OCR_IMAGE_TOKEN_UPPER_BOUND", tc.upper)
			got, err := imageTokenBudgetFromEnv()
			if (err == nil) != tc.valid {
				t.Fatalf("configuration validity: err=%v", err)
			}
			if tc.name == "bound" && (got.modelID != "cq1/qwen3.6" || got.imageTokens != 128000 || got.providerURL != tc.provider) {
				t.Fatalf("configuration was not bound exactly: %+v", got)
			}
		})
	}
}

func TestImageTokenBudgetCountsOnlyValidatedBoundOCR(t *testing.T) {
	imageBytes := model.SampleWarrantyPNG
	payload := testOCRPayload(t, "cq1/qwen3.6", "image/png", "请读取全部原文", imageBytes, 1)
	budget := imageTokenBudget{providerURL: "http://127.0.0.1:3425/v1", modelID: "cq1/qwen3.6", imageTokens: 128000}
	upstream := upstreamConfig{ProviderURL: budget.providerURL, OCRModel: "panda-local/cq1/qwen3.6"}
	want := int64(len(payload)-base64.StdEncoding.EncodedLen(len(imageBytes))) + budget.imageTokens
	if got := budget.inputUpperBound(payload, "cq1/qwen3.6", upstream, 200000); got != want {
		t.Fatalf("verified image reservation=%d, want %d", got, want)
	}
	var jpegImage bytes.Buffer
	if err := jpeg.Encode(&jpegImage, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	jpegPayload := testOCRPayload(t, "cq1/qwen3.6", "image/jpeg", "读取文字", jpegImage.Bytes(), 1)
	jpegWant := int64(len(jpegPayload)-base64.StdEncoding.EncodedLen(jpegImage.Len())) + budget.imageTokens
	if got := budget.inputUpperBound(jpegPayload, "cq1/qwen3.6", upstream, 200000); got != jpegWant {
		t.Fatalf("verified JPEG reservation=%d, want %d", got, jpegWant)
	}
	animation := make([]byte, 20)
	binary.BigEndian.PutUint32(animation[:4], 8)
	copy(animation[4:8], "acTL")
	binary.BigEndian.PutUint32(animation[8:12], 2)
	binary.BigEndian.PutUint32(animation[16:20], crc32.ChecksumIEEE(animation[4:16]))
	animatedPNG := append(append(append([]byte(nil), imageBytes[:33]...), animation...), imageBytes[33:]...)
	concatenatedPNG := append(append([]byte(nil), imageBytes...), imageBytes...)
	for _, tc := range []struct {
		name      string
		budget    imageTokenBudget
		upstream  upstreamConfig
		requested string
		body      []byte
	}{
		{"disabled", imageTokenBudget{}, upstream, "cq1/qwen3.6", payload},
		{"provider changed", budget, upstreamConfig{ProviderURL: "http://other.invalid/v1", OCRModel: upstream.OCRModel}, "cq1/qwen3.6", payload},
		{"configured model changed", budget, upstreamConfig{ProviderURL: budget.providerURL, OCRModel: "other"}, "cq1/qwen3.6", payload},
		{"requested model changed", budget, upstream, "other", payload},
		{"multiple images", budget, upstream, "cq1/qwen3.6", testOCRPayload(t, "cq1/qwen3.6", "image/png", "text", imageBytes, 2)},
		{"wrong media", budget, upstream, "cq1/qwen3.6", testOCRPayload(t, "cq1/qwen3.6", "image/gif", "text", imageBytes, 1)},
		{"duplicate key", budget, upstream, "cq1/qwen3.6", bytes.Replace(payload, []byte(`{"model":`), []byte(`{"model":"other","model":`), 1)},
		{"not an image", budget, upstream, "cq1/qwen3.6", testOCRPayload(t, "cq1/qwen3.6", "image/png", "text", []byte("not-a-png"), 1)},
		{"truncated image", budget, upstream, "cq1/qwen3.6", testOCRPayload(t, "cq1/qwen3.6", "image/png", "text", imageBytes[:50], 1)},
		{"wrong image declaration", budget, upstream, "cq1/qwen3.6", testOCRPayload(t, "cq1/qwen3.6", "image/jpeg", "text", imageBytes, 1)},
		{"animated PNG", budget, upstream, "cq1/qwen3.6", testOCRPayload(t, "cq1/qwen3.6", "image/png", "text", animatedPNG, 1)},
		{"concatenated PNG", budget, upstream, "cq1/qwen3.6", testOCRPayload(t, "cq1/qwen3.6", "image/png", "text", concatenatedPNG, 1)},
		{"extra JSON field", budget, upstream, "cq1/qwen3.6", bytes.Replace(payload, []byte(`{"model":`), []byte(`{"extra":1,"model":`), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.budget.inputUpperBound(tc.body, tc.requested, tc.upstream, 200000); got != int64(len(tc.body)) {
				t.Fatalf("unverified request changed its byte reservation: got=%d want=%d", got, len(tc.body))
			}
		})
	}
}

func TestLargeOCRBudgetPreservesPostgresReservationAndSettlement(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	imageBytes := testImageBudget(t)
	st, admin := openGatewayTestStore(t)
	caseID, _ := createGatewayCase(t, st, admin, "large OCR budget boundary")
	documentID, err := st.AddDocument(context.Background(), admin, caseID, "attachment", "budget.png", "image/png", imageBytes)
	if err != nil {
		t.Fatal(err)
	}
	config := gatewayTestConfig("fixture", "")
	config.ProviderURL = "http://127.0.0.1:3425/v1"
	config.OCRModel = "panda-local/cq1/qwen3.6"
	config.FixtureOCRContent = "合成响应"
	config.Budget.MaxInputTokenReservation = 200000
	config.Budget.MaxOutputTokenReservation = 8192
	config.imageBudget = imageTokenBudget{providerURL: config.ProviderURL, modelID: "cq1/qwen3.6", imageTokens: 128000}
	_, gateway := startGatewayServer(t, st, config)
	scope := encodedGatewayScope(t, documentGatewayScope(admin, caseID, documentID))
	client := &http.Client{}
	send := func(endpoint string, payload []byte) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Contract-Service-Key", config.DocumentCredential)
		req.Header.Set("X-Audit-Execution-Scope", scope)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode
	}
	payload := testOCRPayload(t, "cq1/qwen3.6", "image/png", "只转写图片中的文字", imageBytes, 1)
	if len(payload) <= 200000 {
		t.Fatal("OCR payload did not exceed the original byte limit")
	}
	if status := send(gateway.URL, payload); status != http.StatusOK {
		t.Fatalf("valid large OCR request status=%d", status)
	}
	usage, active := readGatewayAccounting(t, caseID)
	wantInput := int64(len(payload)-base64.StdEncoding.EncodedLen(len(imageBytes))) + 128000
	if len(usage) != 1 || usage[0].Caller != "document" || usage[0].Status != "completed" ||
		!usage[0].ReservedInput.Valid || usage[0].ReservedInput.Int64 != wantInput ||
		!usage[0].ReservedOutput.Valid || usage[0].ReservedOutput.Int64 != 8192 {
		t.Fatalf("large OCR reservation did not reach the durable ledger: %+v want input=%d", usage, wantInput)
	}
	assertUnknownUsageConservativelyCharged(t, usage[0])
	assertNoActiveGatewayLeases(t, active)

	mismatchProvider := config
	mismatchProvider.imageBudget.providerURL = "http://different.invalid/v1"
	_, providerGateway := startGatewayServer(t, st, mismatchProvider)
	mismatchModel := config
	mismatchModel.imageBudget.modelID = "other-ocr"
	_, modelGateway := startGatewayServer(t, st, mismatchModel)
	disabled := config
	disabled.imageBudget = imageTokenBudget{}
	_, disabledGateway := startGatewayServer(t, st, disabled)
	globalLimited := config
	globalLimited.Budget.GlobalTokensPerMinute = 128000
	_, globalGateway := startGatewayServer(t, st, globalLimited)
	tenantLimited := config
	tenantLimited.Budget.TenantTokensPerMinute = 128000
	_, tenantGateway := startGatewayServer(t, st, tenantLimited)

	invalid := append([]byte(nil), payload...)
	marker := []byte(";base64,")
	index := bytes.Index(invalid, marker)
	if index < 0 {
		t.Fatal("test payload lacks image data")
	}
	invalid[index+len(marker)] = '*'
	for _, tc := range []struct {
		name, endpoint string
		body           []byte
		want           int
	}{
		{"provider identity", providerGateway.URL, payload, http.StatusBadRequest},
		{"model identity", modelGateway.URL, payload, http.StatusBadRequest},
		{"disabled", disabledGateway.URL, payload, http.StatusBadRequest},
		{"wrong requested model", gateway.URL, testOCRPayload(t, "fixture-chat", "image/png", "text", imageBytes, 1), http.StatusBadRequest},
		{"two images", gateway.URL, testOCRPayload(t, "cq1/qwen3.6", "image/png", "text", imageBytes, 2), http.StatusBadRequest},
		{"unsupported media", gateway.URL, testOCRPayload(t, "cq1/qwen3.6", "image/gif", "text", imageBytes, 1), http.StatusBadRequest},
		{"invalid base64", gateway.URL, invalid, http.StatusBadRequest},
		{"duplicate model", gateway.URL, bytes.Replace(payload, []byte(`{"model":`), []byte(`{"model":"other","model":`), 1), http.StatusBadRequest},
		{"text plus image bound", gateway.URL, testOCRPayload(t, "cq1/qwen3.6", "image/png", strings.Repeat("x", 80000), model.SampleWarrantyPNG, 1), http.StatusBadRequest},
		{"global budget", globalGateway.URL, payload, http.StatusTooManyRequests},
		{"tenant budget", tenantGateway.URL, payload, http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if status := send(tc.endpoint, tc.body); status != tc.want {
				t.Fatalf("response status=%d want=%d", status, tc.want)
			}
		})
	}
	usage, active = readGatewayAccounting(t, caseID)
	if len(usage) != 1 {
		t.Fatalf("rejected requests reached durable dispatch: rows=%d", len(usage))
	}
	assertNoActiveGatewayLeases(t, active)
}
