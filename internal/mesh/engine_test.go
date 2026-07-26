package mesh

import (
	"strings"
	"testing"
)

func TestEnvironmentWithoutRemovesOnlyNamedVariables(t *testing.T) {
	t.Setenv("MESH_IDENTITY_SEED", "private")
	t.Setenv("MESH_TEST_VISIBLE", "public")
	environment := environmentWithout("MESH_IDENTITY_SEED")
	for _, item := range environment {
		if strings.HasPrefix(item, "MESH_IDENTITY_SEED=") {
			t.Fatal("identity seed remained in child environment")
		}
	}
	if !environmentContains(environment, "MESH_TEST_VISIBLE=public") {
		t.Fatal("unrelated environment variable was removed")
	}
}

func environmentContains(environment []string, wanted string) bool {
	for _, item := range environment {
		if item == wanted {
			return true
		}
	}
	return false
}
