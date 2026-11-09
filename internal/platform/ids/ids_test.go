package ids

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", true},
		{" AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA ", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", true},
		{"not-a-uuid", "", false},
		{"", "", false},
		{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa' OR '1'='1", "", false},
		{"aaaaaaaaaaaa4aaa8aaaaaaaaaaaaaaa", "", false},
	}

	for _, tt := range tests {
		got, ok := Normalize(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("Normalize(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestNormalizeAll(t *testing.T) {
	got, ok := NormalizeAll([]string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
	})
	if !ok || len(got) != 2 {
		t.Fatalf("NormalizeAll removed duplicates incorrectly: %v, %v", got, ok)
	}

	if _, ok := NormalizeAll([]string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "x"}); ok {
		t.Error("NormalizeAll must reject a list with an invalid id")
	}

	empty, ok := NormalizeAll(nil)
	if !ok || empty == nil || len(empty) != 0 {
		t.Errorf("NormalizeAll(nil) = %v, %v; want empty non-nil slice", empty, ok)
	}
}
