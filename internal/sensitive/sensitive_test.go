package sensitive

import "testing"

func TestPath(t *testing.T) {
	for _, p := range []string{".env", "a/.env.local", "id_rsa", "x/k.PEM", "credentials.json", ".npmrc", "SECRET_x", "a\\.env"} {
		if !Path(p) {
			t.Errorf("%s should be sensitive", p)
		}
	}
	for _, p := range []string{"main.go", "a/b.txt", "environment.go"} {
		if Path(p) {
			t.Errorf("%s should not be sensitive", p)
		}
	}
}
