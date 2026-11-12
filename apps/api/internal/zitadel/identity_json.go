package zitadel

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

func decodeIdentityJSON(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return ErrIdentityUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkIdentityJSON(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrIdentityUnavailable
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrIdentityUnavailable
	}
	return nil
}

func walkIdentityJSON(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return ErrIdentityUnavailable
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrIdentityUnavailable
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	var closing json.Delim
	switch delim {
	case '{':
		closing = '}'
		seen := map[string]struct{}{}
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return ErrIdentityUnavailable
			}
			key = identityJSONKey(key)
			if _, duplicate := seen[key]; duplicate {
				return ErrIdentityUnavailable
			}
			seen[key] = struct{}{}
			if err := walkIdentityJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		closing = ']'
		for decoder.More() {
			if err := walkIdentityJSON(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrIdentityUnavailable
	}
	end, err := decoder.Token()
	if err != nil || end != closing {
		return ErrIdentityUnavailable
	}
	return nil
}

func identityJSONKey(key string) string {
	// encoding/json matches struct fields through Unicode simple folding too.
	var folded strings.Builder
	for _, r := range key {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		folded.WriteRune(minimum)
	}
	return folded.String()
}
