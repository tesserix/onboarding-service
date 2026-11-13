package main

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/staffcontract"
)

func runtimeIdentityContracts() string {
	var b strings.Builder
	for _, model := range []any{staffcontract.StaffIdentityCreateInput{}, staffcontract.StaffIdentityCurrentState{}, staffcontract.StaffIdentityReceipt{}, staffcontract.StaffIdentityObservationReceipt{}} {
		typ := reflect.TypeOf(model)
		fmt.Fprintf(&b, "\nexport const %sContract = z.object({\n", typ.Name())
		for _, field := range reflect.VisibleFields(typ) {
			tags := strings.Split(field.Tag.Get("json"), ",")
			value := field.Type
			if value.Kind() == reflect.Pointer {
				value = value.Elem()
			}
			var schema string
			switch {
			case value == reflect.TypeFor[uuid.UUID]():
				schema = "z.uuid()"
			case value == reflect.TypeFor[time.Time]():
				schema = "z.iso.datetime({offset: true})"
			case value.Kind() == reflect.String:
				schema = "z.string().min(1)"
			case value.Kind() == reflect.Bool:
				schema = "z.boolean()"
			case value.Kind() == reflect.Int:
				schema = "z.number().int()"
			default:
				schema = value.Name() + "Contract"
			}
			optional := false
			for _, tag := range tags[1:] {
				if tag == "omitempty" {
					optional = true
				}
			}
			if optional {
				schema += ".optional()"
			} else if field.Type.Kind() == reflect.Pointer {
				schema += ".nullable()"
			}
			fmt.Fprintf(&b, "  %s: %s,\n", tags[0], schema)
		}
		fmt.Fprintf(&b, "}).strict() satisfies z.ZodType<%s>;\n", typ.Name())
	}
	b.WriteString("\nexport type StaffContactInput = Omit<StaffIdentityCreateInput, \"actor_subject\">;\nexport const StaffContactInputContract = StaffIdentityCreateInputContract.omit({actor_subject: true});\n")
	return b.String()
}
