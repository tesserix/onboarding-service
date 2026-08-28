package onboarding

import (
	"reflect"
	"testing"
)

func TestRegistryCredentialInputIsValidatedAndCanonicalized(t *testing.T) {
	got, err := validateRegistryCredentialInput(RegistryCredentialInput{
		Name: " CI publisher ", LifetimeDays: 30,
		Scopes:     []string{"registry:publish", "registry:read"},
		Namespaces: []string{"team-b", "team-a"},
		Kinds:      []string{"Tool", "Agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "CI publisher" ||
		!reflect.DeepEqual(got.Scopes, []string{"registry:publish", "registry:read"}) ||
		!reflect.DeepEqual(got.Namespaces, []string{"team-a", "team-b"}) ||
		!reflect.DeepEqual(got.Kinds, []string{"Agent", "Tool"}) {
		t.Fatalf("input was not canonicalized: %#v", got)
	}
}

func TestRegistryCredentialInputRejectsAuthorityExpansion(t *testing.T) {
	valid := RegistryCredentialInput{
		Name: "CI", LifetimeDays: 30, Scopes: []string{"registry:read"},
		Namespaces: []string{"team-a"}, Kinds: []string{"Agent"},
	}
	tests := map[string]func(*RegistryCredentialInput){
		"unknown scope":     func(in *RegistryCredentialInput) { in.Scopes = []string{"registry:admin"} },
		"duplicate scope":   func(in *RegistryCredentialInput) { in.Scopes = []string{"registry:read", "registry:read"} },
		"invalid namespace": func(in *RegistryCredentialInput) { in.Namespaces = []string{"../platform"} },
		"unknown kind":      func(in *RegistryCredentialInput) { in.Kinds = []string{"Secret"} },
		"long lifetime":     func(in *RegistryCredentialInput) { in.LifetimeDays = 91 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			if _, err := validateRegistryCredentialInput(input); err == nil {
				t.Fatal("unsafe credential input was accepted")
			}
		})
	}
}

func TestRegistryScopesMapOnlyToLeastPrivilegeProjectRoles(t *testing.T) {
	got := rolesForRegistryScopes([]string{"registry:read", "registry:delete"})
	want := []string{"registry.reader", "registry.deleter"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %#v, want %#v", got, want)
	}
}
