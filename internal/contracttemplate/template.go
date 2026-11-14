// File metadata (manifest mirror):
// Purpose: Validates bounded tenant contract-template fields and renders nonrecursive plain-text placeholders; defines exact template projections for the authenticated Core/Document boundary.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Package contracttemplate validates and renders plain text contract templates.
package contracttemplate

import (
	"errors"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}
type Content struct {
	Body   string  `json:"body"`
	Fields []Field `json:"fields"`
}

var fieldKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var decimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

// Validate only admits literal text and simple named substitutions; no
// expressions, functions, external references or nested template operations.
func Validate(c Content) error {
	if !utf8.ValidString(c.Body) || strings.TrimSpace(c.Body) == "" || strings.ContainsRune(c.Body, '\x00') || utf8.RuneCountInString(c.Body) > 30000 || len(c.Fields) == 0 || len(c.Fields) > 32 {
		return errors.New("invalid template body or field count")
	}
	fields := map[string]bool{}
	for _, f := range c.Fields {
		if !fieldKey.MatchString(f.Key) || fields[f.Key] || strings.TrimSpace(f.Label) == "" || utf8.RuneCountInString(f.Label) > 100 || !utf8.ValidString(f.Label) || strings.IndexFunc(f.Label, unicode.IsControl) >= 0 || !f.Required {
			return errors.New("invalid template field")
		}
		switch f.Type {
		case "string", "decimal", "date":
		default:
			return errors.New("unsupported template field type")
		}
		fields[f.Key] = true
	}
	seen := map[string]bool{}
	for rest := c.Body; rest != ""; {
		start := strings.Index(rest, "{{")
		close := strings.Index(rest, "}}")
		if start < 0 {
			if close >= 0 {
				return errors.New("unmatched template delimiter")
			}
			break
		}
		if close >= 0 && close < start {
			return errors.New("unmatched template delimiter")
		}
		rest = rest[start+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return errors.New("unmatched template delimiter")
		}
		key := rest[:end]
		if !fields[key] {
			return errors.New("unknown template field")
		}
		seen[key] = true
		rest = rest[end+2:]
	}
	for key := range fields {
		if !seen[key] {
			return errors.New("unused template field")
		}
	}
	return nil
}

// Render performs a single nonrecursive replacement after validating every
// supplied value. Plain-text fields cannot add line breaks or placeholders.
func Render(c Content, input map[string]string) (string, error) {
	if err := Validate(c); err != nil {
		return "", err
	}
	if len(input) != len(c.Fields) {
		return "", errors.New("template field set differs")
	}
	substitutions := make([]string, 0, len(c.Fields)*2)
	for _, f := range c.Fields {
		raw, ok := input[f.Key]
		value := strings.TrimSpace(raw)
		if !ok || value == "" || !utf8.ValidString(raw) || utf8.RuneCountInString(value) > 500 || strings.ContainsAny(value, "{}") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
			return "", errors.New("invalid template value")
		}
		switch f.Type {
		case "decimal":
			if len(value) > 64 || !decimal.MatchString(value) {
				return "", errors.New("invalid decimal value")
			}
			if _, ok := new(big.Rat).SetString(value); !ok {
				return "", errors.New("invalid decimal value")
			}
		case "date":
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return "", errors.New("invalid date value")
			}
		}
		substitutions = append(substitutions, "{{"+f.Key+"}}", value)
	}
	return strings.NewReplacer(substitutions...).Replace(c.Body), nil
}

// Resolved is a trusted Core projection of one active immutable template.
type Resolved struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Digest  string  `json:"digest"`
	Content Content `json:"content"`
}

// ResolveRequest is sent only over the authenticated Document-to-Core channel.
type ResolveRequest struct {
	SessionDigest  string `json:"session_digest"`
	ContextVersion string `json:"context_version"`
	CaseID         string `json:"case_id"`
	ID             string `json:"id,omitempty"`
	Version        string `json:"version,omitempty"`
	Digest         string `json:"digest,omitempty"`
}

// ReservedID protects the two platform identities from tenant collisions.
func ReservedID(id string) bool {
	return id == "ent_telecom_dedicated_line_v1" || id == "ent_cloud_services_v1"
}
