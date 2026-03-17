package template

import "testing"

func TestEngine_Render(t *testing.T) {
	e := NewEngine()

	result, err := e.Render("greeting", "Hello {{.Name}}, your code is {{.Code}}", map[string]interface{}{
		"Name": "Alice",
		"Code": "1234",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "Hello Alice, your code is 1234"
	if result != expected {
		t.Errorf("got %q, want %q", result, expected)
	}
}

func TestEngine_RenderCached(t *testing.T) {
	e := NewEngine()

	// First render parses
	_, err := e.Render("cached", "Hi {{.Name}}", map[string]interface{}{"Name": "Bob"})
	if err != nil {
		t.Fatal(err)
	}

	// Second render uses cache
	result, err := e.Render("cached", "Hi {{.Name}}", map[string]interface{}{"Name": "Charlie"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "Hi Charlie" {
		t.Errorf("got %q, want %q", result, "Hi Charlie")
	}
}

func TestEngine_Invalidate(t *testing.T) {
	e := NewEngine()

	_, err := e.Render("inv", "V1: {{.X}}", map[string]interface{}{"X": "a"})
	if err != nil {
		t.Fatal(err)
	}

	e.Invalidate("inv")

	result, err := e.Render("inv", "V2: {{.X}}", map[string]interface{}{"X": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "V2: b" {
		t.Errorf("got %q, want %q", result, "V2: b")
	}
}

func TestEngine_RenderInvalidTemplate(t *testing.T) {
	e := NewEngine()

	_, err := e.Render("bad", "{{.Unclosed", nil)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}
