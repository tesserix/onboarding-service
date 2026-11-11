package customercontract

import (
	"net/mail"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BusinessContact is the organisation's business contact, independent of its login owner.
type BusinessContact struct {
	Phone string `json:"phone" required:"true" max:"16" clean:"true" pattern:"^\\+[1-9][0-9]{7,14}$"`
	Email string `json:"email" required:"true" max:"200" clean:"true" format:"email"`
}

// BusinessAddress is a physical headquarters or initial branch address.
type BusinessAddress struct {
	Line1       string    `json:"line1" required:"true" max:"200" clean:"true"`
	Line2       string    `json:"line2" max:"200" clean:"true"`
	City        string    `json:"city" required:"true" max:"100" clean:"true"`
	District    string    `json:"district" required:"true" max:"100" clean:"true"`
	PostalCode  string    `json:"postalCode" required:"true" pattern:"^[1-9][0-9]{5}$"`
	StateCode   StateCode `json:"stateCode"`
	CountryCode Country   `json:"countryCode"`
}

// MainBranch identifies the initial operating branch and its physical address.
type MainBranch struct {
	Name    string          `json:"name" required:"true" max:"150" clean:"true"`
	Address BusinessAddress `json:"address"`
}

// OrganizationProfile carries submitted business contacts and distinct headquarters and branch facts.
type OrganizationProfile struct {
	Contact    BusinessContact `json:"contact"`
	HeadOffice BusinessAddress `json:"headOffice"`
	MainBranch MainBranch      `json:"mainBranch"`
}

// BusinessProfileView returns submitted organisation facts under the business owner's resource scope.
type BusinessProfileView struct {
	Profile *OrganizationProfile `json:"profile" nullable:"true"`
}

// Validate checks every supplied profile before durable intake or business provisioning.
func (p OrganizationProfile) Validate() error {
	if !profileStringsValid(p.Contact) || !profileStringsValid(p.MainBranch) || p.HeadOffice.Validate() != nil || p.MainBranch.Address.Validate() != nil {
		return ErrInvalid
	}
	address, err := mail.ParseAddress(p.Contact.Email)
	if err != nil || address.Address != p.Contact.Email {
		return ErrInvalid
	}
	return nil
}

// Validate enforces the supported address scope and bounded physical-address fields.
func (a BusinessAddress) Validate() error {
	if !profileStringsValid(a) || !slices.Contains(a.CountryCode.Values(), string(a.CountryCode)) || !slices.Contains(a.StateCode.Values(), string(a.StateCode)) {
		return ErrInvalid
	}
	return nil
}

func profileStringsValid(input any) bool {
	value := reflect.ValueOf(input)
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if value.Field(i).Kind() != reflect.String {
			continue
		}
		text := value.Field(i).String()
		if field.Tag.Get("required") == "true" && text == "" {
			return false
		}
		if field.Tag.Get("clean") == "true" && (!utf8.ValidString(text) || strings.TrimSpace(text) != text || strings.ContainsFunc(text, unicode.IsControl)) {
			return false
		}
		if limit := field.Tag.Get("max"); limit != "" {
			max, err := strconv.Atoi(limit)
			if err != nil || utf8.RuneCountInString(text) > max {
				return false
			}
		}
		if pattern := field.Tag.Get("pattern"); pattern != "" && !regexp.MustCompile(pattern).MatchString(text) {
			return false
		}
	}
	return true
}
