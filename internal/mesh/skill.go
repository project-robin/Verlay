package mesh

import _ "embed"

//go:embed skill/skill.md
var agentSkill string

func AgentSkill() string { return agentSkill }
