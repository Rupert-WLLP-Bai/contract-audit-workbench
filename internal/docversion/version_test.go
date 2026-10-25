package docversion_test

import (
	"errors"
	"testing"

	"contractaudit/internal/docversion"
)

func TestEmptyFileDigest(t *testing.T) {
	const emptySHA = docversion.Digest("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")

	fromNil, err := docversion.New("nil", docversion.Source{Origin: "upload"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fromEmpty, err := docversion.New("empty", docversion.Source{Origin: "upload"}, []byte{})
	if err != nil {
		t.Fatal(err)
	}
	if fromNil.Digest() != emptySHA || fromEmpty.Digest() != emptySHA {
		t.Fatalf("nil %s empty %s", fromNil.Digest(), fromEmpty.Digest())
	}
	if fromNil.Size() != 0 || fromEmpty.Size() != 0 {
		t.Fatalf("sizes %d %d", fromNil.Size(), fromEmpty.Size())
	}
	if !fromNil.SameContent(fromEmpty) {
		t.Fatal("nil and empty bytes are the same original file")
	}
}

func TestKnownSHA256Vector(t *testing.T) {
	got := docversion.Hash([]byte("abc"))
	const want = docversion.Digest("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
	if got != want {
		t.Fatalf("Hash(abc) = %s", got)
	}
}

func TestReuploadIdenticalBytes(t *testing.T) {
	body := []byte("采购文件正文\n质保36个月")
	first, err := docversion.New("ver-1", docversion.Source{
		Origin:    "upload:batch-a",
		Filename:  "procurement.pdf",
		MediaType: "application/pdf",
	}, body)
	if err != nil {
		t.Fatal(err)
	}
	again, err := docversion.New("ver-2", docversion.Source{
		Origin:    "reupload:batch-b",
		Filename:  "copy.pdf",
		MediaType: "application/octet-stream",
	}, append([]byte(nil), body...))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() == again.ID() {
		t.Fatal("reupload keeps a distinct version id")
	}
	if first.Digest() != again.Digest() || !first.SameContent(again) {
		t.Fatalf("digest changed on reupload: %s vs %s", first.Digest(), again.Digest())
	}
	if err := first.Verify(body); err != nil {
		t.Fatal(err)
	}
	if err := again.Verify(body); err != nil {
		t.Fatal(err)
	}
}

func TestDigestIsRawBytes(t *testing.T) {
	base := []byte("合同正文")
	spaced := []byte("合同正文 ")
	a, err := docversion.New("a", docversion.Source{Origin: "upload"}, base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := docversion.New("b", docversion.Source{Origin: "upload"}, spaced)
	if err != nil {
		t.Fatal(err)
	}
	if a.SameContent(b) {
		t.Fatal("trailing byte must change the original-file digest")
	}

	han, err := docversion.New("c", docversion.Source{Origin: "upload"}, []byte("中"))
	if err != nil {
		t.Fatal(err)
	}
	if han.Size() != 3 {
		t.Fatalf("size counts bytes, got %d", han.Size())
	}
}

func TestNewRejectsEmptyIdentity(t *testing.T) {
	_, err := docversion.New("  ", docversion.Source{Origin: "upload"}, []byte("x"))
	if !errors.Is(err, docversion.ErrEmptyID) {
		t.Fatalf("empty id: %v", err)
	}
	_, err = docversion.New("id", docversion.Source{Origin: " \t"}, []byte("x"))
	if !errors.Is(err, docversion.ErrEmptySource) {
		t.Fatalf("empty origin: %v", err)
	}
	_, err = docversion.New("", docversion.Source{}, nil)
	if !errors.Is(err, docversion.ErrEmptyID) {
		t.Fatalf("missing id: %v", err)
	}
}

func TestVerifyRejectsTamperedBytes(t *testing.T) {
	body := []byte("abcdef")
	v, err := docversion.New("v", docversion.Source{Origin: "upload"}, body)
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]byte{
		[]byte("abcdeg"),
		[]byte("abcde"),
		[]byte("abcdef\n"),
		nil,
	}
	for _, content := range cases {
		if err := v.Verify(content); !errors.Is(err, docversion.ErrDigestMismatch) {
			t.Fatalf("Verify(%q) = %v", content, err)
		}
	}
}

func TestParseDigest(t *testing.T) {
	const good = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got, err := docversion.ParseDigest(good)
	if err != nil || got != docversion.Digest(good) {
		t.Fatalf("parse good: %s %v", got, err)
	}
	for _, bad := range []string{
		"",
		good[:63],
		good + "aa",
		"BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD",
		"zz7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
	} {
		if _, err := docversion.ParseDigest(bad); !errors.Is(err, docversion.ErrInvalidDigest) {
			t.Fatalf("ParseDigest(%q) = %v", bad, err)
		}
	}
}

func TestZeroVersionIsNotVerifiable(t *testing.T) {
	var v docversion.Version
	if err := v.Verify(nil); !errors.Is(err, docversion.ErrInvalidDigest) {
		t.Fatalf("zero Verify = %v", err)
	}
	if v.SameContent(v) {
		t.Fatal("zero version has no content identity")
	}
}

func TestSourceIsRetainedAndDoesNotAffectDigest(t *testing.T) {
	body := []byte("same-bytes")
	src := docversion.Source{Origin: "  inbox  ", Filename: " a.docx ", MediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}
	v, err := docversion.New("doc-1", src, body)
	if err != nil {
		t.Fatal(err)
	}
	src.Origin = "changed-later"
	src.Filename = "other.docx"
	got := v.Source()
	if got.Origin != "inbox" || got.Filename != " a.docx " {
		t.Fatalf("source = %+v", got)
	}
	other, err := docversion.New("doc-2", docversion.Source{Origin: "elsewhere"}, body)
	if err != nil {
		t.Fatal(err)
	}
	if v.Digest() != other.Digest() {
		t.Fatal("source metadata changed the content digest")
	}
}
