package main

import (
	"testing"

	"github.com/aca/entish/lang"
)

func TestConfig(t *testing.T) {
	if err := lang.New(lang.Options{}).Check(config, "config.ent"); err != nil {
		t.Fatal(err)
	}
}
