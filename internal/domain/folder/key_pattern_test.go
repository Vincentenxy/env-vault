package folder

import "testing"

func TestMatchKeyPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		key     string
		want    bool
	}{
		{name: "disabled", pattern: "", key: "any.key-value", want: true},
		{name: "uppercase preset", pattern: `^[A-Z][A-Z0-9_]*$`, key: "DB_PASSWORD_1", want: true},
		{name: "uppercase preset mismatch", pattern: `^[A-Z][A-Z0-9_]*$`, key: "db-password", want: false},
		{name: "unanchored expression still full match", pattern: `foo`, key: "prefix-foo", want: false},
		{name: "alternation still full match", pattern: `A|AB`, key: "AB", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchKeyPattern(tt.pattern, tt.key)
			if err != nil {
				t.Fatalf("MatchKeyPattern() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("MatchKeyPattern() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateKeyPattern_Invalid(t *testing.T) {
	if err := ValidateKeyPattern("["); err == nil {
		t.Fatal("ValidateKeyPattern() expected invalid expression error")
	}
}
