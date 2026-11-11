package customercontract

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
)

// DecodeIdentityProjection rejects duplicate, recased and unknown wire fields.
// Callers apply their transport body limit before decoding.
func DecodeIdentityProjection(raw []byte) (IdentityProjection, error) {
	var view IdentityProjection
	if strictProjectionObject(raw, reflect.TypeFor[IdentityProjection]()) != nil || json.Unmarshal(raw, &view) != nil || view.Validate() != nil {
		return IdentityProjection{}, ErrInvalid
	}
	return view, nil
}

func strictProjectionObject(raw []byte, kind reflect.Type) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	fields := make(map[string]reflect.Type, kind.NumField())
	for i := range kind.NumField() {
		field := kind.Field(i)
		fields[strings.Split(field.Tag.Get("json"), ",")[0]] = field.Type
	}
	seen := make(map[string]bool, len(fields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return ErrInvalid
		}
		name, ok := token.(string)
		if !ok || seen[name] {
			return ErrInvalid
		}
		field, ok := fields[name]
		if !ok {
			return ErrInvalid
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ErrInvalid
		}
		if field.Kind() == reflect.Pointer {
			field = field.Elem()
		}
		if field.Kind() == reflect.Struct && strictProjectionObject(value, field) != nil {
			return ErrInvalid
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
