package repository

import (
	"encoding/json"
	"testing"
)

func TestStringArray_MarshalJSON_Nil(t *testing.T) {
	var a StringArray
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal nil: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("nil marshal = %q, want []", b)
	}
}

func TestStringArray_MarshalJSON_Empty(t *testing.T) {
	a := StringArray{}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(b) != "[]" {
		t.Errorf("empty marshal = %q, want []", b)
	}
}

func TestStringArray_MarshalJSON_Values(t *testing.T) {
	a := StringArray{"text", "tools", "vision"}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `["text","tools","vision"]` {
		t.Errorf("marshal = %q, want [\"text\",\"tools\",\"vision\"]", b)
	}
}

func TestStringArray_UnmarshalJSON_Array(t *testing.T) {
	var a StringArray
	if err := json.Unmarshal([]byte(`["text","tools"]`), &a); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(a) != 2 || a[0] != "text" || a[1] != "tools" {
		t.Errorf("unmarshal result = %v, want [text tools]", a)
	}
}

func TestStringArray_UnmarshalJSON_EmptyArray(t *testing.T) {
	var a StringArray
	if err := json.Unmarshal([]byte(`[]`), &a); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if a == nil {
		t.Error("empty array unmarshal = nil, want non-nil empty slice")
	}
	if len(a) != 0 {
		t.Errorf("empty array unmarshal len = %d, want 0", len(a))
	}
}

func TestStringArray_UnmarshalJSON_Null(t *testing.T) {
	var a StringArray
	if err := json.Unmarshal([]byte(`null`), &a); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if a == nil {
		t.Error("null unmarshal = nil, want non-nil empty slice")
	}
	if len(a) != 0 {
		t.Errorf("null unmarshal len = %d, want 0", len(a))
	}
}

func TestStringArray_UnmarshalJSON_TypeMismatch(t *testing.T) {
	var a StringArray
	if err := json.Unmarshal([]byte(`"hello"`), &a); err == nil {
		t.Error("string unmarshal should fail")
	}
	if err := json.Unmarshal([]byte(`123`), &a); err == nil {
		t.Error("number unmarshal should fail")
	}
	if err := json.Unmarshal([]byte(`{"a":1}`), &a); err == nil {
		t.Error("object unmarshal should fail")
	}
}

func TestStringArray_Scan_Nil(t *testing.T) {
	var a StringArray
	if err := a.Scan(nil); err != nil {
		t.Fatalf("scan nil: %v", err)
	}
	if a == nil {
		t.Error("scan nil = nil, want non-nil empty slice")
	}
	if len(a) != 0 {
		t.Errorf("scan nil len = %d, want 0", len(a))
	}
}

func TestStringArray_Scan_Bytes(t *testing.T) {
	var a StringArray
	if err := a.Scan([]byte(`["text","vision"]`)); err != nil {
		t.Fatalf("scan bytes: %v", err)
	}
	if len(a) != 2 || a[0] != "text" || a[1] != "vision" {
		t.Errorf("scan result = %v, want [text vision]", a)
	}
}

func TestStringArray_Scan_EmptyBytes(t *testing.T) {
	var a StringArray
	if err := a.Scan([]byte{}); err != nil {
		t.Fatalf("scan empty bytes: %v", err)
	}
	if a == nil {
		t.Error("scan empty bytes = nil, want non-nil empty slice")
	}
}

func TestStringArray_Scan_InvalidType(t *testing.T) {
	var a StringArray
	if err := a.Scan("not bytes"); err == nil {
		t.Error("scan string should fail")
	}
}

func TestStringArray_Value_Nil(t *testing.T) {
	var a StringArray
	v, err := a.Value()
	if err != nil {
		t.Fatalf("value nil: %v", err)
	}
	b, ok := v.([]byte)
	if !ok {
		t.Fatalf("value type = %T, want []byte", v)
	}
	if string(b) != "[]" {
		t.Errorf("value nil = %q, want []", b)
	}
}

func TestStringArray_Value_Values(t *testing.T) {
	a := StringArray{"text", "tools"}
	v, err := a.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	b, ok := v.([]byte)
	if !ok {
		t.Fatalf("value type = %T, want []byte", v)
	}
	if string(b) != `["text","tools"]` {
		t.Errorf("value = %q, want [\"text\",\"tools\"]", b)
	}
}

func TestStringArray_RoundTrip(t *testing.T) {
	original := StringArray{"text", "tools", "vision"}
	// Marshal → Unmarshal
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored StringArray
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(restored) != len(original) {
		t.Fatalf("round-trip len = %d, want %d", len(restored), len(original))
	}
	for i := range original {
		if restored[i] != original[i] {
			t.Errorf("round-trip[%d] = %q, want %q", i, restored[i], original[i])
		}
	}
}

func TestStringArray_ScanValueRoundTrip(t *testing.T) {
	original := StringArray{"text", "tools"}
	// Value → Scan
	v, err := original.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	b, _ := v.([]byte)
	var restored StringArray
	if err := restored.Scan(b); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(restored) != len(original) {
		t.Fatalf("scan/value round-trip len = %d, want %d", len(restored), len(original))
	}
	for i := range original {
		if restored[i] != original[i] {
			t.Errorf("scan/value round-trip[%d] = %q, want %q", i, restored[i], original[i])
		}
	}
}

// Model JSON decode test: the original bug — capabilities as string array
// must decode without error.
func TestModel_JSONDecode_CapabilitiesArray(t *testing.T) {
	raw := `{"id":"gpt-4o","display_name":"GPT-4o","capabilities":["text","tools"],"thinking_supported":false,"status":"active"}`
	var m Model
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode model with capabilities array: %v", err)
	}
	if len(m.Capabilities) != 2 || m.Capabilities[0] != "text" || m.Capabilities[1] != "tools" {
		t.Errorf("capabilities = %v, want [text tools]", m.Capabilities)
	}
}

func TestModel_JSONEncode_CapabilitiesArray(t *testing.T) {
	m := Model{
		ID:           "gpt-4o",
		DisplayName:  "GPT-4o",
		Capabilities: StringArray{"text", "tools"},
		Status:       "active",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode model: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	caps, ok := out["capabilities"].([]any)
	if !ok {
		t.Fatalf("capabilities type = %T, want []any", out["capabilities"])
	}
	if len(caps) != 2 {
		t.Errorf("capabilities len = %d, want 2", len(caps))
	}
	if caps[0] != "text" || caps[1] != "tools" {
		t.Errorf("capabilities = %v, want [text tools]", caps)
	}
}

func TestModel_JSONDecode_EmptyCapabilities(t *testing.T) {
	raw := `{"id":"x","display_name":"X","capabilities":[],"thinking_supported":false,"status":"active"}`
	var m Model
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode model with empty capabilities: %v", err)
	}
	if m.Capabilities == nil {
		t.Error("empty capabilities = nil, want non-nil empty slice")
	}
	if len(m.Capabilities) != 0 {
		t.Errorf("empty capabilities len = %d, want 0", len(m.Capabilities))
	}
}

func TestModel_JSONDecode_MissingCapabilities(t *testing.T) {
	raw := `{"id":"x","display_name":"X","thinking_supported":false,"status":"active"}`
	var m Model
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode model without capabilities: %v", err)
	}
	// Missing field leaves StringArray at nil (zero value); MarshalJSON handles nil as [].
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	caps, ok := out["capabilities"].([]any)
	if !ok {
		t.Fatalf("capabilities type = %T, want []any", out["capabilities"])
	}
	if len(caps) != 0 {
		t.Errorf("missing capabilities len = %d, want 0", len(caps))
	}
}

func TestModel_JSONDecode_InputOutputModalities(t *testing.T) {
	raw := `{"id":"x","display_name":"X","input_modalities":["text","image"],"output_modalities":["text"],"capabilities":["text"],"thinking_supported":false,"status":"active"}`
	var m Model
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m.InputModalities) != 2 || m.InputModalities[0] != "text" || m.InputModalities[1] != "image" {
		t.Errorf("input_modalities = %v, want [text image]", m.InputModalities)
	}
	if len(m.OutputModalities) != 1 || m.OutputModalities[0] != "text" {
		t.Errorf("output_modalities = %v, want [text]", m.OutputModalities)
	}
}
