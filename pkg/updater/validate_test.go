package updater

import "testing"

// D-AUDIT-76: чужой URL и traversal в имени бинаря отклоняются.
func TestValidateReleaseSource(t *testing.T) {
	prod := GetProdReleaseAPIURL()

	cases := []struct {
		name    string
		url     string
		binary  string
		wantErr bool
	}{
		{"default empty url", "", "picoclaw-launcher", false},
		{"official url", prod, "picoclaw-launcher", false},
		{"official url with tag", prod + "/tags/v1", "picoclaw-launcher", false},
		{"foreign host", "https://evil.com/pwn.zip", "picoclaw-launcher", true},
		{"lookalike host", prod[:len(prod)-1] + "x.evil.com", "picoclaw-launcher", true},
		{"empty binary", prod, "", true},
		{"traversal binary", prod, "../../etc/pwn", true},
		{"slash binary", prod, "dir/pwn", true},
		{"backslash binary", prod, "dir\\pwn", true},
	}
	for _, tc := range cases {
		err := ValidateReleaseSource(tc.url, tc.binary)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}
