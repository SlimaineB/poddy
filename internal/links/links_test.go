package links

import "testing"

func TestNormalizeAndValidate(t *testing.T) {
	values := Normalize([]Link{
		{Name: " JetBrains Gateway ", URL: "jetbrains-gateway://connect#host={sshHost}", Icon: "jetbrains"},
		{Name: " Docs ", URL: " https://example.com/docs/{name} ", NewTab: true},
		{Name: " ", URL: " "},
	})
	if len(values) != 2 {
		t.Fatalf("got %d links", len(values))
	}
	if values[0].Icon != "jetbrains" || !values[1].NewTab || values[1].Icon != "external" {
		t.Fatalf("unexpected links: %+v", values)
	}
	if err := Validate(values); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{
		"javascript:alert(1)",
		"data:text/html,hello",
		"vbscript:msgbox(1)",
		"https://",
		"not a url",
	} {
		if err := Validate([]Link{{Name: "Bad", URL: raw}}); err == nil {
			t.Errorf("expected %q to be rejected", raw)
		}
	}
}

func TestValidateRejectsDuplicateNames(t *testing.T) {
	values := []Link{
		{Name: "Docs", URL: "https://example.com"},
		{Name: "docs", URL: "https://example.com"},
	}
	if err := Validate(values); err == nil {
		t.Fatal("expected duplicate name error")
	}
}
