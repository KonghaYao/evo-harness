package ingest

import (
	"testing"
)

func TestParseHarborFilesUsageAndPassAt1(t *testing.T) {
	job := SynthJob{
		ID:   "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Name: "tiny",
		Trials: []SynthTrial{
			{ID: "11111111-1111-4111-8111-111111111111", Name: "a", Checksum: "x", Reward: 1},
			{ID: "11111111-1111-4111-8111-111111111112", Name: "b", Checksum: "y", Reward: 1},
			{ID: "11111111-1111-4111-8111-111111111113", Name: "c", Checksum: "z", Reward: 0},
		},
	}
	files, err := job.Files()
	if err != nil {
		t.Fatal(err)
	}
	root, stripped := StripCommonRoot(files)
	if root != "tiny" {
		t.Fatalf("root %q", root)
	}
	parsed, err := ParseHarborFiles(job.ID, root, stripped)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Job.NTrials != 3 || parsed.Job.NReward1 != 2 {
		t.Fatalf("pass@1 inputs %d/%d", parsed.Job.NReward1, parsed.Job.NTrials)
	}
	if parsed.Job.UsageReported {
		t.Fatal("all-null usage must set usage_reported=false; zeros are persisted placeholders, not measured zeros")
	}
	if parsed.Job.NInputTokens != 0 || parsed.Job.CostUSD != 0 || parsed.Job.NAgentSteps != 0 {
		t.Fatalf("expected persisted zeros, got %+v", parsed.Job)
	}
	if parsed.Job.AgentName != "peri" {
		t.Fatalf("majority agent_name %q", parsed.Job.AgentName)
	}
	if parsed.Job.AgentVersion == nil || *parsed.Job.AgentVersion != "agent-v3.14.2" {
		t.Fatalf("majority agent_version %v", parsed.Job.AgentVersion)
	}
	if parsed.Job.ModelName == nil || *parsed.Job.ModelName != "deepseek-v4-flash" {
		t.Fatalf("majority model_name %v", parsed.Job.ModelName)
	}
	if parsed.Job.ModelProvider == nil || *parsed.Job.ModelProvider != "deepseek" {
		t.Fatalf("majority model_provider %v", parsed.Job.ModelProvider)
	}
}

func TestParseHarborFilesMajorityAgent(t *testing.T) {
	job := SynthJob{
		ID:   "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab",
		Name: "mixed-agent",
		Trials: []SynthTrial{
			{ID: "11111111-1111-4111-8111-111111111121", Name: "a", Checksum: "x", Reward: 1, AgentName: "peri"},
			{ID: "11111111-1111-4111-8111-111111111122", Name: "b", Checksum: "y", Reward: 0, AgentName: "peri"},
			{ID: "11111111-1111-4111-8111-111111111123", Name: "c", Checksum: "z", Reward: 0, AgentName: "other"},
		},
	}
	files, err := job.Files()
	if err != nil {
		t.Fatal(err)
	}
	root, stripped := StripCommonRoot(files)
	parsed, err := ParseHarborFiles(job.ID, root, stripped)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Job.AgentName != "peri" {
		t.Fatalf("majority want peri got %q", parsed.Job.AgentName)
	}
}

func TestParseHarborFilesRejectsMissingChecksum(t *testing.T) {
	files := map[string][]byte{
		"result.json": []byte(`{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`),
		"t1/result.json": []byte(`{
			"id":"11111111-1111-4111-8111-111111111111",
			"job_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			"task_name":"x",
			"agent_info":{"name":"peri"},
			"verifier_result":{"rewards":{"reward":1}}
		}`),
	}
	_, err := ParseHarborFiles("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "j", files)
	if err == nil {
		t.Fatal("expected validation error")
	}
	ve, ok := err.(*ValidateError)
	if !ok {
		t.Fatalf("got %T", err)
	}
	if ve.Message != "trial_invalid" && ve.Message != "trial missing task_checksum" {
		t.Fatalf("message %s", ve.Message)
	}
}

func TestParseHarborFilesConfigJobID(t *testing.T) {
	jobID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	files := map[string][]byte{
		"result.json": []byte(`{"id":"` + jobID + `"}`),
		"t-pass/result.json": []byte(`{
			"id":"11111111-1111-4111-8111-111111111111",
			"task_name":"hello",
			"task_checksum":"abababababababababababababababababababababababababababababababab",
			"config":{"job_id":"` + jobID + `"},
			"agent_info":{"name":"peri","version":"agent-v3.14.2","model_info":{"name":"deepseek-v4-flash","provider":"deepseek"}},
			"verifier_result":{"rewards":{"reward":1}}
		}`),
	}
	parsed, err := ParseHarborFiles(jobID, "j", files)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Job.NTrials != 1 || parsed.Job.NReward1 != 1 {
		t.Fatalf("parsed %+v", parsed.Job)
	}
}
