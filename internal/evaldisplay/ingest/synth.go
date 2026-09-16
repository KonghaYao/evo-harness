package ingest

import (
	"encoding/json"
	"fmt"
	"time"
)

type SynthTrial struct {
	ID            string
	Name          string
	TaskName      string
	Checksum      string
	Reward        float64
	Usage         *Usage // nil => all usage fields JSON null (not measured zero)
	StartedAt     time.Time
	FinishedAt    time.Time
	EnvStart      time.Time
	EnvEnd        time.Time
	AgentName     string
	AgentVersion  string
	ModelName     string
	ModelProvider string
}

type SynthJob struct {
	ID       string
	Name     string
	Trials   []SynthTrial
	JobUsage *Usage
}

func (j SynthJob) Files() (map[string][]byte, error) {
	root := j.Name
	if root == "" {
		root = "job"
	}
	files := map[string][]byte{}
	jobResult := map[string]any{
		"id":     j.ID,
		"status": "completed",
	}
	if j.JobUsage != nil {
		jobResult["n_input_tokens"] = j.JobUsage.NInputTokens
		jobResult["n_cache_tokens"] = j.JobUsage.NCacheTokens
		jobResult["n_output_tokens"] = j.JobUsage.NOutputTokens
		jobResult["cost_usd"] = j.JobUsage.CostUSD
		jobResult["n_agent_steps"] = j.JobUsage.NAgentSteps
	}
	jb, err := json.Marshal(jobResult)
	if err != nil {
		return nil, err
	}
	files[root+"/result.json"] = jb
	files[root+"/config.json"] = []byte(`{"environment":{"type":"docker"}}`)
	files[root+"/lock.json"] = []byte(`{"name":"pier","version":"0.3.1"}`)
	for _, t := range j.Trials {
		raw, err := t.resultJSON(j.ID)
		if err != nil {
			return nil, err
		}
		prefix := root + "/" + t.Name
		files[prefix+"/result.json"] = raw
		files[prefix+"/config.json"] = []byte(`{"agent":{"name":null}}`)
		files[prefix+"/lock.json"] = []byte(`{"name":"pier","version":"0.3.1"}`)
	}
	return files, nil
}

func (j SynthJob) TarGz() ([]byte, error) {
	files, err := j.Files()
	if err != nil {
		return nil, err
	}
	return PackTarGz(files)
}

func (t SynthTrial) resultJSON(jobID string) ([]byte, error) {
	agentResult := map[string]any{
		"n_input_tokens":  nil,
		"n_cache_tokens":  nil,
		"n_output_tokens": nil,
		"cost_usd":        nil,
		"n_agent_steps":   nil,
	}
	if t.Usage != nil {
		agentResult["n_input_tokens"] = t.Usage.NInputTokens
		agentResult["n_cache_tokens"] = t.Usage.NCacheTokens
		agentResult["n_output_tokens"] = t.Usage.NOutputTokens
		agentResult["cost_usd"] = t.Usage.CostUSD
		agentResult["n_agent_steps"] = t.Usage.NAgentSteps
	}
	started := t.StartedAt
	finished := t.FinishedAt
	if started.IsZero() {
		started = time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	}
	if finished.IsZero() {
		finished = started.Add(10 * time.Minute)
	}
	env0, env1 := t.EnvStart, t.EnvEnd
	if env0.IsZero() {
		env0 = started
	}
	if env1.IsZero() {
		env1 = started.Add(30 * time.Second)
	}
	task := t.TaskName
	if task == "" {
		task = "datacurve/" + t.Name
	}
	checksum := t.Checksum
	if checksum == "" {
		checksum = fmt.Sprintf("sum-%s", t.ID)
	}
	obj := map[string]any{
		"id":            t.ID,
		"job_id":        jobID,
		"task_name":     task,
		"task_checksum": checksum,
		"trial_name":    t.Name,
		"agent_info": map[string]any{
			"name":    firstNonEmpty(t.AgentName, "peri"),
			"version": firstNonEmpty(t.AgentVersion, "agent-v3.14.2"),
			"model_info": map[string]any{
				"name":     firstNonEmpty(t.ModelName, "deepseek-v4-flash"),
				"provider": firstNonEmpty(t.ModelProvider, "deepseek"),
			},
		},
		"verifier_result": map[string]any{
			"rewards": map[string]any{"reward": t.Reward, "f2p": nil, "p2p": nil},
		},
		"agent_result":   agentResult,
		"exception_info": nil,
		"started_at":     started.Format(time.RFC3339Nano),
		"finished_at":    finished.Format(time.RFC3339Nano),
		"environment_setup": map[string]any{
			"started_at":  env0.Format(time.RFC3339Nano),
			"finished_at": env1.Format(time.RFC3339Nano),
		},
		"agent_setup": map[string]any{
			"started_at":  env1.Format(time.RFC3339Nano),
			"finished_at": env1.Add(200 * time.Millisecond).Format(time.RFC3339Nano),
		},
		"agent_execution": map[string]any{
			"started_at":  env1.Add(200 * time.Millisecond).Format(time.RFC3339Nano),
			"finished_at": finished.Add(-time.Minute).Format(time.RFC3339Nano),
		},
		"verifier": map[string]any{
			"started_at":  finished.Add(-time.Minute).Format(time.RFC3339Nano),
			"finished_at": finished.Format(time.RFC3339Nano),
		},
	}
	return json.Marshal(obj)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
