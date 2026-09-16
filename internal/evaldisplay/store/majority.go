package store

// AgentIdentity is the majority (agent, version, model, provider) tuple for a job.
type AgentIdentity struct {
	AgentName     string
	AgentVersion  *string
	ModelName     *string
	ModelProvider *string
	Consistent    bool
}

// MajorityAgentIdentity picks the most common agent_info tuple. Ties keep the
// first winner in slice order (ingest sorts trials by directory name).
func MajorityAgentIdentity(trials []AnalysisTrial) AgentIdentity {
	type key struct {
		name, ver, model, prov string
	}
	counts := map[key]int{}
	var best key
	bestN := -1
	for _, t := range trials {
		k := key{t.AgentName, derefStr(t.AgentVersion), derefStr(t.ModelName), derefStr(t.ModelProvider)}
		counts[k]++
		if counts[k] > bestN {
			bestN = counts[k]
			best = k
		}
	}
	id := AgentIdentity{AgentName: best.name, Consistent: len(counts) <= 1}
	if best.ver != "" {
		id.AgentVersion = &best.ver
	}
	if best.model != "" {
		id.ModelName = &best.model
	}
	if best.prov != "" {
		id.ModelProvider = &best.prov
	}
	return id
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func applyAgentIdentity(job *AnalysisJob, id AgentIdentity) {
	job.AgentName = id.AgentName
	job.AgentVersion = id.AgentVersion
	job.ModelName = id.ModelName
	job.ModelProvider = id.ModelProvider
}
