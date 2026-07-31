package models

import (
	"errors"
	"testing"
)

func TestNotificationActionValidate(t *testing.T) {
	valid := NotificationAction{Type: "link", Label: "Open settings", Href: "/panel/settings?tab=profile#security"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid action rejected: %v", err)
	}

	for _, action := range []NotificationAction{
		{Type: "button", Label: "Open", Href: "/panel"},
		{Type: "link", Label: " \t", Href: "/panel"},
		{Type: "link", Label: "Open", Href: "javascript:alert(1)"},
		{Type: "link", Label: "Open", Href: "https://example.test"},
		{Type: "link", Label: "Open", Href: "//example.test"},
		{Type: "link", Label: "Open", Href: "/\\example.test"},
		{Type: "link", Label: "Open", Href: "/panel\nsettings"},
	} {
		if err := action.Validate(); !errors.Is(err, ErrInvalidNotificationAction) {
			t.Errorf("Validate(%+v) error = %v, want ErrInvalidNotificationAction", action, err)
		}
	}
}

func TestNotificationActionPtrScanRejectsUnsafeStoredActions(t *testing.T) {
	for _, raw := range []string{
		`{"type":"link","label":"Open","href":"javascript:alert(1)"}`,
		`{"type":"link","label":"Open","href":"//example.test"}`,
		`{"type":"link","label":"Open","href":"/panel","href":"javascript:alert(1)"}`,
		`{"type":"link","label":"Open","href":"/panel","extra":true}`,
	} {
		var action NotificationActionPtr
		if err := action.Scan([]byte(raw)); !errors.Is(err, ErrInvalidNotificationAction) {
			t.Errorf("Scan(%s) error = %v, want ErrInvalidNotificationAction", raw, err)
		}
	}
}

func TestNotificationActionPtrValueRejectsUnsafeAction(t *testing.T) {
	action := NotificationActionPtr{Action: &NotificationAction{Type: "link", Label: "Open", Href: "https://example.test"}}
	if _, err := action.Value(); !errors.Is(err, ErrInvalidNotificationAction) {
		t.Fatalf("Value() error = %v, want ErrInvalidNotificationAction", err)
	}
}
