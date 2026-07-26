// Package skills exposes the Agent Skills shipped with Verlay to the Go
// runtime. Keeping the embedded copy here makes skills/verlay/SKILL.md the
// single source of truth for both agent installers and `verlay skill print`.
package skills

import _ "embed"

//go:embed verlay/SKILL.md
var verlay string

// Verlay returns the standard Verlay Agent Skill.
func Verlay() string { return verlay }
