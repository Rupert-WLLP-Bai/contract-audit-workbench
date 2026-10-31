package legalnorm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

var articleHeading = regexp.MustCompile(`^第([〇零一二三四五六七八九十百千万亿\d]+)条[\s　]*(.*)$`)

type extractedArticle struct {
	number int
	label  string
	body   string
	start  int
	end    int
}

// VerifyCapture selects a registered source edition, verifies the raw and
// registry response identities, and checks asserted articles against the
// versioned extraction. New source captures require an explicit adapter.
func VerifyCapture(input CaptureInput) (VerifiedCapture, error) {
	adapter, err := adapterForSource(input.SourceID, input.VersionID)
	if err != nil {
		return VerifiedCapture{}, err
	}
	return verifyCaptureWithAdapter(input, adapter)
}

func verifyCaptureWithAdapter(input CaptureInput, adapter sourceAdapter) (VerifiedCapture, error) {
	if err := validateCaptureMetadata(input, adapter); err != nil {
		return VerifiedCapture{}, err
	}
	rawDigest := digest(input.RawBytes)
	if !equalDigest(input.ExpectedRawSHA256, rawDigest) {
		return VerifiedCapture{}, fmt.Errorf("raw response SHA-256 mismatch")
	}
	statusDigest := digest(input.StatusEvidenceRaw)
	if !equalDigest(input.ExpectedStatusSHA256, statusDigest) {
		return VerifiedCapture{}, fmt.Errorf("status evidence SHA-256 mismatch")
	}
	status, err := verifyStatusEvidence(input, adapter)
	if err != nil {
		return VerifiedCapture{}, err
	}
	if input.CurrencyStatus != status {
		return VerifiedCapture{}, fmt.Errorf("asserted registry status %q disagrees with source-derived status %q", input.CurrencyStatus, status)
	}

	blocks, err := htmlTextBlocks(input.RawBytes, input.ContentType)
	if err != nil {
		return VerifiedCapture{}, fmt.Errorf("decode or parse source HTML: %w", err)
	}
	if len(blocks) == 0 {
		return VerifiedCapture{}, fmt.Errorf("source HTML contains no readable text blocks")
	}
	if err := applyEditorialAnnotations(blocks, input, adapter); err != nil {
		return VerifiedCapture{}, err
	}
	fullText := NormalizeWhitespace(strings.Join(blocks, " "))
	if fullText == "" {
		return VerifiedCapture{}, fmt.Errorf("source HTML has empty extracted text")
	}
	articles, err := extractArticles(blocks)
	if err != nil {
		return VerifiedCapture{}, err
	}
	if len(articles) == 0 {
		return VerifiedCapture{}, fmt.Errorf("source HTML contains no formal article headings")
	}
	verifiedArticles, err := verifyArticleAssertions(input, articles)
	if err != nil {
		return VerifiedCapture{}, err
	}
	return VerifiedCapture{
		SourceID: input.SourceID, SourceSnapshotID: input.SourceSnapshotID, VersionID: input.VersionID,
		Title: input.Title, OfficialURL: input.OfficialURL, CapturedContentType: input.ContentType,
		RawSHA256: rawDigest, SourceCapturedAt: input.SourceCapturedAt, FullText: fullText, FullTextSHA256: digest([]byte(fullText)),
		ExtractorID: adapter.extractorID, ExtractorVersion: adapter.extractorVersion,
		CanonicalTextVersion: adapter.canonicalizationVersion,
		PromulgatedOn:        input.PromulgatedOn, EffectiveFrom: input.EffectiveFrom, EffectiveUntil: input.EffectiveUntil,
		CurrencyStatus: input.CurrencyStatus, StatusEvidenceID: input.StatusEvidenceID,
		StatusObservedAt: input.StatusObservedAt, StatusEvidenceSHA256: statusDigest,
		Articles: verifiedArticles,
	}, nil
}

func validateCaptureMetadata(input CaptureInput, adapter sourceAdapter) error {
	if input.SourceID == "" || input.SourceSnapshotID == "" || input.VersionID == "" || input.Title == "" || input.SourceCapturedAt == "" ||
		input.OfficialURL == "" || len(input.RawBytes) == 0 || len(input.StatusEvidenceRaw) == 0 || len(input.Articles) == 0 {
		return fmt.Errorf("capture identity, raw bytes, status evidence and article assertions are required")
	}
	if input.SourceID != adapter.sourceID || input.VersionID != adapter.versionID ||
		input.Title != adapter.title || input.OfficialURL != adapter.officialURL {
		return fmt.Errorf("capture metadata does not match the registered official source edition")
	}
	mediaType, params, err := mime.ParseMediaType(input.ContentType)
	if err != nil || mediaType != "text/html" {
		return fmt.Errorf("unsupported source content type %q", input.ContentType)
	}
	if charsetName := strings.ToLower(params["charset"]); charsetName != "" && charsetName != "utf-8" && charsetName != "utf8" {
		return fmt.Errorf("unsupported source charset %q", charsetName)
	}
	if input.PromulgatedOn != adapter.promulgatedOn || input.EffectiveFrom != adapter.effectiveFrom {
		return fmt.Errorf("capture promulgation/effective dates do not match the registered edition")
	}
	if adapter.pinEffectiveUntil && input.EffectiveUntil != adapter.effectiveUntil {
		return fmt.Errorf("capture effective-until date does not match the registered edition")
	}
	if input.StatusEvidenceID == "" || input.ExpectedStatusSHA256 == "" {
		return fmt.Errorf("status evidence identity and digest are required")
	}
	if !digestSyntax(input.ExpectedRawSHA256) || !digestSyntax(input.ExpectedStatusSHA256) {
		return fmt.Errorf("source and status SHA-256 values must be lowercase hex digests")
	}
	if err := validateDateRange(input.PromulgatedOn, input.EffectiveFrom, input.EffectiveUntil); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, input.SourceCapturedAt); err != nil {
		return fmt.Errorf("invalid source capture time: %w", err)
	}
	observedAt, err := time.Parse(time.RFC3339, input.StatusObservedAt)
	if err != nil {
		return fmt.Errorf("invalid status observation time: %w", err)
	}
	effectiveAt, _ := time.Parse("2006-01-02", input.EffectiveFrom)
	if observedAt.Before(effectiveAt) {
		return fmt.Errorf("status observation predates the normative effective date")
	}
	if adapter.canonicalizationVersion != CanonicalTextVersion {
		return fmt.Errorf("unsupported canonicalization version %q", adapter.canonicalizationVersion)
	}
	return nil
}

func validateDateRange(promulgatedOn, effectiveFrom, effectiveUntil string) error {
	promulgated, err := time.Parse("2006-01-02", promulgatedOn)
	if err != nil {
		return fmt.Errorf("invalid promulgation date: %w", err)
	}
	effective, err := time.Parse("2006-01-02", effectiveFrom)
	if err != nil {
		return fmt.Errorf("invalid effective-from date: %w", err)
	}
	if effective.Before(promulgated) {
		return fmt.Errorf("effective-from date precedes promulgation")
	}
	if effectiveUntil != "" {
		until, err := time.Parse("2006-01-02", effectiveUntil)
		if err != nil {
			return fmt.Errorf("invalid effective-until date: %w", err)
		}
		if !until.After(effective) {
			return fmt.Errorf("effective-until date must be after effective-from")
		}
	}
	return nil
}

func verifyStatusEvidence(input CaptureInput, adapter sourceAdapter) (string, error) {
	switch adapter.statusFormat {
	case statusNPCJSON:
		var result struct {
			Rows []struct {
				ID     string `json:"bbbs"`
				Title  string `json:"title"`
				Issued string `json:"gbrq"`
				Active string `json:"sxrq"`
				Status int    `json:"sxx"`
			} `json:"rows"`
		}
		if err := json.Unmarshal(input.StatusEvidenceRaw, &result); err != nil {
			return "", fmt.Errorf("invalid NPC status evidence JSON: %w", err)
		}
		for _, row := range result.Rows {
			if adapter.statusRecordID != "" && row.ID != adapter.statusRecordID {
				continue
			}
			if normalizeHTMLFragment(row.Title) != adapter.registryTitle || row.Issued != adapter.promulgatedOn ||
				row.Active != adapter.statusEffectiveAt {
				return "", fmt.Errorf("NPC status record does not match source title or version dates")
			}
			switch row.Status {
			case 1:
				return StatusRepealed, nil
			case 3:
				return StatusEffective, nil
			case 4:
				return StatusNotYetEffective, nil
			default:
				return "", fmt.Errorf("unsupported NPC status code %d", row.Status)
			}
		}
		return "", fmt.Errorf("NPC status evidence does not contain the registered record")
	case statusMOJHTML:
		text, err := htmlPlainText(input.StatusEvidenceRaw, "text/html; charset=utf-8")
		if err != nil {
			return "", fmt.Errorf("invalid MOJ status evidence HTML: %w", err)
		}
		if !strings.Contains(text, "现行有效 1 部") || !strings.Contains(text, adapter.registryTitle) ||
			!strings.Contains(text, adapter.statusEffectiveAt) {
			return "", fmt.Errorf("MOJ status evidence does not show the registered effective norm version")
		}
		return StatusEffective, nil
	case statusFixture:
		var result struct {
			Status     string `json:"status"`
			ObservedAt string `json:"observed_at"`
		}
		if err := json.Unmarshal(input.StatusEvidenceRaw, &result); err != nil {
			return "", fmt.Errorf("invalid fixture status evidence: %w", err)
		}
		if result.ObservedAt != input.StatusObservedAt {
			return "", fmt.Errorf("fixture status evidence disagrees with asserted observation time")
		}
		return result.Status, nil
	default:
		return "", fmt.Errorf("unsupported status evidence format %q", adapter.statusFormat)
	}
}

// The IPC website inserts editorial headings in brackets immediately after
// these five article labels. Remove only the exact documented insertions when
// producing the canonical statutory text; the untouched response remains in
// the immutable raw capture. Offsets refer to the resulting extracted text.
func applyEditorialAnnotations(blocks []string, input CaptureInput, adapter sourceAdapter) error {
	if adapter.extractorID != "spc-ipc-contract-history-html" {
		return nil
	}
	annotations := map[int]string{
		39: "格式合同条款定义及使用人义务", 40: "格式合同条款的无效", 41: "格式合同的解释",
		107: "违约责任", 114: "违约金",
	}
	for _, ref := range input.Articles {
		if _, ok := annotations[ref.ArticleNumber]; !ok {
			return fmt.Errorf("historical article %d has no documented editorial annotation", ref.ArticleNumber)
		}
	}
	for i, block := range blocks {
		match := articleHeading.FindStringSubmatch(block)
		if len(match) == 0 {
			continue
		}
		number, err := parseChineseNumber(match[1])
		if err != nil {
			return fmt.Errorf("invalid historical article label: %w", err)
		}
		annotation, ok := annotations[number]
		if !ok {
			continue
		}
		label := "第" + match[1] + "条"
		marker := "【" + annotation + "】"
		prefix := label + " " + marker
		if !strings.HasPrefix(block, prefix) || strings.Count(block, marker) != 1 {
			return fmt.Errorf("historical article %s editorial annotation differs from the documented source", label)
		}
		blocks[i] = NormalizeWhitespace(label + " " + strings.TrimPrefix(block, prefix))
	}
	return nil
}

func htmlPlainText(raw []byte, contentType string) (string, error) {
	reader, err := charset.NewReader(bytes.NewReader(raw), contentType)
	if err != nil {
		return "", fmt.Errorf("decode HTML charset: %w", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("read decoded HTML: %w", err)
	}
	if !utf8.Valid(decoded) {
		return "", fmt.Errorf("HTML is not valid UTF-8 after charset decoding")
	}
	root, err := html.Parse(bytes.NewReader(decoded))
	if err != nil {
		return "", fmt.Errorf("parse HTML: %w", err)
	}
	var builder strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
			builder.WriteByte(' ')
			return
		}
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return NormalizeWhitespace(builder.String()), nil
}

func htmlTextBlocks(raw []byte, contentType string) ([]string, error) {
	if strings.Contains(strings.ToLower(contentType), "charset=utf-8") && !utf8.Valid(raw) {
		return nil, fmt.Errorf("declared UTF-8 source contains invalid byte sequences")
	}
	reader, err := charset.NewReader(bytes.NewReader(raw), contentType)
	if err != nil {
		return nil, fmt.Errorf("decode HTML charset: %w", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read decoded HTML: %w", err)
	}
	if !utf8.Valid(decoded) {
		return nil, fmt.Errorf("HTML source is not valid UTF-8 after declared-charset decoding")
	}
	root, err := html.Parse(bytes.NewReader(decoded))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}
	blocks := make([]string, 0, 256)
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && isTextBlock(node.Data) {
			var rawText strings.Builder
			appendNodeText(&rawText, node)
			if text := NormalizeWhitespace(rawText.String()); text != "" {
				blocks = append(blocks, text)
			}
			return
		}
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return blocks, nil
}

func isTextBlock(tag string) bool {
	switch tag {
	case "p", "h1", "h2", "h3", "h4", "h5", "h6", "li":
		return true
	default:
		return false
	}
}

func appendNodeText(builder *strings.Builder, node *html.Node) {
	if node.Type == html.TextNode {
		builder.WriteString(node.Data)
		return
	}
	if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style") {
		return
	}
	if node.Type == html.ElementNode && node.Data == "br" {
		builder.WriteByte(' ')
		return
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		appendNodeText(builder, child)
	}
}

func extractArticles(blocks []string) ([]extractedArticle, error) {
	type head struct {
		index  int
		number int
		label  string
	}
	heads := make([]head, 0, 128)
	for index, block := range blocks {
		match := articleHeading.FindStringSubmatch(block)
		if len(match) == 0 {
			continue
		}
		number, err := parseChineseNumber(match[1])
		if err != nil {
			return nil, fmt.Errorf("invalid formal article label %q: %w", match[1], err)
		}
		label := "第" + match[1] + "条"
		heads = append(heads, head{index: index, number: number, label: label})
	}
	if len(heads) == 0 {
		return nil, nil
	}
	articles := make([]extractedArticle, 0, len(heads))
	blockStarts := make([]int, len(blocks))
	position := 0
	for i, block := range blocks {
		blockStarts[i] = position
		position += utf8.RuneCountInString(block)
		if i+1 < len(blocks) {
			position++ // the canonical full text joins blocks with one space
		}
	}
	for i, current := range heads {
		endIndex := len(blocks)
		if i+1 < len(heads) {
			endIndex = heads[i+1].index
		}
		for boundary := current.index + 1; boundary < endIndex; boundary++ {
			if isArticleContentBoundary(blocks[boundary]) {
				endIndex = boundary
				break
			}
		}
		body := NormalizeWhitespace(strings.Join(blocks[current.index:endIndex], " "))
		start := blockStarts[current.index]
		end := start + utf8.RuneCountInString(body)
		articles = append(articles, extractedArticle{number: current.number, label: current.label, body: body, start: start, end: end})
	}
	return articles, nil
}

func isArticleContentBoundary(block string) bool {
	text := NormalizeWhitespace(block)
	chapter := regexp.MustCompile(`^第[〇零一二三四五六七八九十百千万亿\d]+章(?:\s|$)`)
	if chapter.MatchString(text) {
		return true
	}
	for _, prefix := range []string{
		"附：", "附件：", "网站声明", "联系我们", "版权所有", "网站地图", "备案号", "相关文章", "采购头条", "政采动态", "购买服务",
		"主办单位：", "链接：", "国务院部门网站", "中央网络安全和信息化委员会办公室", "中华人民共和国司法部 版权所有",
		"返回顶部", "电脑版", "客户端", "小程序",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func verifyArticleAssertions(input CaptureInput, extracted []extractedArticle) ([]VerifiedArticle, error) {
	byNumber := make(map[int]extractedArticle, len(extracted))
	for _, item := range extracted {
		if _, exists := byNumber[item.number]; exists {
			return nil, fmt.Errorf("source contains duplicate formal article number %d", item.number)
		}
		byNumber[item.number] = item
	}
	seen := make(map[int]struct{}, len(input.Articles))
	verified := make([]VerifiedArticle, 0, len(input.Articles))
	for _, ref := range input.Articles {
		if ref.SourceID != input.SourceID || ref.SourceSnapshotID != input.SourceSnapshotID || ref.VersionID != input.VersionID {
			return nil, fmt.Errorf("article reference identity does not match its source capture")
		}
		if _, duplicate := seen[ref.ArticleNumber]; duplicate {
			return nil, fmt.Errorf("duplicate article assertion %d", ref.ArticleNumber)
		}
		seen[ref.ArticleNumber] = struct{}{}
		if err := validateArticleMetadata(ref, input); err != nil {
			return nil, fmt.Errorf("article %s metadata: %w", ref.ArticleLabel, err)
		}
		found, ok := byNumber[ref.ArticleNumber]
		if !ok {
			return nil, fmt.Errorf("source does not contain article %s", ref.ArticleLabel)
		}
		if ref.ArticleLabel != found.label {
			return nil, fmt.Errorf("article label %q does not match source label %q", ref.ArticleLabel, found.label)
		}
		wantText := NormalizeWhitespace(ref.FullText)
		if wantText == "" || wantText != found.body {
			return nil, fmt.Errorf("article %s text differs from server-extracted source text at rune %d: extracted %q; expected %q", ref.ArticleLabel, firstRuneDifference(found.body, wantText), excerptAtRune(found.body, firstRuneDifference(found.body, wantText)), excerptAtRune(wantText, firstRuneDifference(found.body, wantText)))
		}
		if !digestSyntax(ref.TextSHA256) || !equalDigest(ref.TextSHA256, digest([]byte(wantText))) {
			return nil, fmt.Errorf("article %s text SHA-256 mismatch", ref.ArticleLabel)
		}
		if ref.CitationSpan.Quote != "" {
			if err := validateSpan(wantText, ref.CitationSpan); err != nil {
				return nil, fmt.Errorf("article %s citation span: %w", ref.ArticleLabel, err)
			}
		} else if ref.CitationSpan.Start != 0 || ref.CitationSpan.End != 0 {
			return nil, fmt.Errorf("article %s has offsets without a quote", ref.ArticleLabel)
		}
		verified = append(verified, VerifiedArticle{Ref: ref, FullText: found.body, TextSHA256: digest([]byte(found.body)), Start: found.start, End: found.end})
	}
	return verified, nil
}

func firstRuneDifference(left, right string) int {
	a, b := []rune(left), []rune(right)
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

func excerptAtRune(value string, offset int) string {
	runes := []rune(value)
	start, end := offset-32, offset+64
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

func validateArticleMetadata(ref ArticleRef, input CaptureInput) error {
	if ref.ArticleNumber <= 0 || ref.ArticleLabel == "" || ref.FullText == "" || ref.TextSHA256 == "" {
		return fmt.Errorf("article number, label, body, and digest are required")
	}
	match := articleHeading.FindStringSubmatch(NormalizeWhitespace(ref.ArticleLabel))
	if len(match) == 0 {
		return fmt.Errorf("invalid formal article label")
	}
	number, err := parseChineseNumber(match[1])
	if err != nil || number != ref.ArticleNumber || NormalizeWhitespace(match[2]) != "" {
		return fmt.Errorf("article number does not agree with formal label")
	}
	if ref.PromulgatedOn != input.PromulgatedOn || ref.EffectiveFrom != input.EffectiveFrom || ref.EffectiveUntil != input.EffectiveUntil ||
		ref.CurrencyStatus != input.CurrencyStatus || ref.StatusEvidenceID != input.StatusEvidenceID {
		return fmt.Errorf("article dates/status evidence do not match the source capture")
	}
	return validateDateRange(ref.PromulgatedOn, ref.EffectiveFrom, ref.EffectiveUntil)
}

func validateSpan(text string, span TextSpan) error {
	runes := []rune(text)
	if span.Start < 0 || span.End <= span.Start || span.End > len(runes) {
		return fmt.Errorf("span offsets are outside the article text")
	}
	if string(runes[span.Start:span.End]) != span.Quote {
		return fmt.Errorf("quoted text does not match the stated Unicode offsets")
	}
	return nil
}

func parseChineseNumber(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("empty number")
	}
	if n, err := strconv.Atoi(value); err == nil {
		return n, nil
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int{'十': 10, '百': 100, '千': 1000}
	total, section, number := 0, 0, 0
	for _, char := range value {
		if digit, ok := digits[char]; ok {
			number = digit
			continue
		}
		unit, ok := units[char]
		if !ok {
			if char == '万' {
				section = (section + number) * 10000
				total += section
				section, number = 0, 0
				continue
			}
			return 0, fmt.Errorf("unsupported numeral character %q", char)
		}
		if number == 0 {
			number = 1
		}
		section += number * unit
		number = 0
	}
	result := total + section + number
	if result <= 0 {
		return 0, fmt.Errorf("article number must be positive")
	}
	return result, nil
}

func normalizeHTMLFragment(fragment string) string {
	root, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return NormalizeWhitespace(fragment)
	}
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return NormalizeWhitespace(builder.String())
}

func digest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func digestSyntax(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func equalDigest(left, right string) bool {
	return digestSyntax(left) && digestSyntax(right) && left == right
}
