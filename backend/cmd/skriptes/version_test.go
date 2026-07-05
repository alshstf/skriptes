package main

import "testing"

// TestEffectiveVersion — впечённая версия сборки приоритетнее env, «v» срезается,
// «dev»/пусто → fallback на env (кейс прода: env=latest → показываем впечённый тег).
func TestEffectiveVersion(t *testing.T) {
	orig := version
	defer func() { version = orig }()

	cases := []struct {
		baked, env, want string
	}{
		{"v1.9.0", "latest", "1.9.0"}, // прод: образ latest, но впечён тег → точная версия
		{"1.9.0", "latest", "1.9.0"},  // без ведущего v — тоже ок
		{"dev", "latest", "latest"},   // локальная сборка без бампа → fallback на env
		{"", "1.2.3", "1.2.3"},        // не впечено → env
		{"dev", "dev", "dev"},         // всё дефолт
	}
	for _, c := range cases {
		version = c.baked
		if got := effectiveVersion(c.env); got != c.want {
			t.Errorf("effectiveVersion(baked=%q, env=%q) = %q, want %q", c.baked, c.env, got, c.want)
		}
	}
}
