package overlay

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"evo-harness/internal/evaldisplay/store"
)

type Service struct {
	Store *store.Store
}

func New(st *store.Store) *Service { return &Service{Store: st} }

type PutError struct {
	Code    string
	Message string
}

func (e *PutError) Error() string { return e.Message }

func (s *Service) Get(ctx context.Context, jobID string) (*store.Overlay, error) {
	return s.Store.GetOverlay(ctx, jobID)
}

func (s *Service) Put(ctx context.Context, jobID string, body map[string]any) (*store.Overlay, error) {
	if _, err := s.Store.GetAnalysisJob(ctx, jobID); err != nil {
		return nil, err
	}
	cur, err := s.Store.GetOverlay(ctx, jobID)
	if err != nil {
		return nil, err
	}
	filled := body
	if inner, ok := body["filled"].(map[string]any); ok {
		filled = inner
	}
	if _, ok := filled["pass_at_1"]; ok {
		return nil, &PutError{Code: "overlay_readonly", Message: "pass_at_1 is derived and cannot be written"}
	}
	for _, k := range []string{"n_input_tokens", "n_cache_tokens", "n_output_tokens", "cost_usd", "n_agent_steps"} {
		if _, ok := filled[k]; ok {
			return nil, &PutError{Code: "overlay_readonly", Message: k + " is extracted from Harbor JSON and cannot be overwritten"}
		}
	}

	known := map[string]struct{}{
		"runner": {}, "sandbox": {}, "endpoint_class": {}, "job_type": {}, "job_type_reason": {},
		"attestation": {}, "attestor": {}, "hub_org": {}, "harbor_version": {},
		"dataset_name": {}, "dataset_version": {}, "dataset_ref": {}, "dataset_path": {},
		"incomparability": {}, "agent": {}, "model": {}, "n": {}, "schema": {}, "notes": {},
		"source_archive": {}, "jobs": {}, "still_missing": {}, "trajectory": {},
		"listed": {}, "published": {}, "attestation_status": {}, "runner_name": {}, "runner_version": {},
		"sandbox_type": {}, "sandbox_location": {},
	}
	extra := map[string]any{}
	if cur.Extra != "" && cur.Extra != "{}" {
		_ = json.Unmarshal([]byte(cur.Extra), &extra)
	}

	if r, ok := filled["runner"].(map[string]any); ok {
		if v, _ := r["name"].(string); v != "" {
			cur.RunnerName = &v
		}
		if v, _ := r["version"].(string); v != "" {
			cur.RunnerVersion = &v
		}
	}
	if v, _ := filled["runner_name"].(string); v != "" {
		cur.RunnerName = &v
	}
	if v, _ := filled["runner_version"].(string); v != "" {
		cur.RunnerVersion = &v
	}
	if sb, ok := filled["sandbox"].(map[string]any); ok {
		if v, _ := sb["type"].(string); v != "" {
			cur.SandboxType = &v
		}
		if v, _ := sb["location"].(string); v != "" {
			cur.SandboxLocation = &v
		}
	}
	if v, _ := filled["sandbox_type"].(string); v != "" {
		cur.SandboxType = &v
	}
	if v, _ := filled["sandbox_location"].(string); v != "" {
		cur.SandboxLocation = &v
	}
	if v, exists := filled["endpoint_class"]; exists {
		switch t := v.(type) {
		case nil:
			cur.EndpointClass = nil
		case string:
			if t == "" {
				cur.EndpointClass = nil
			} else {
				cur.EndpointClass = &t
			}
		}
	}
	if m, ok := filled["model"].(map[string]any); ok {
		if v, _ := m["endpoint_class"].(string); v != "" {
			cur.EndpointClass = &v
		}
	}
	if v, exists := filled["job_type"]; exists {
		switch t := v.(type) {
		case nil:
			cur.JobType = nil
		case string:
			if t == "" {
				cur.JobType = nil
			} else if t == "H" || t == "M" {
				cur.JobType = &t
			} else {
				return nil, &PutError{Code: "job_type_invalid", Message: "job_type must be H, M, or null"}
			}
		default:
			return nil, &PutError{Code: "job_type_invalid", Message: "job_type must be H, M, or null"}
		}
	}
	if v, _ := filled["job_type_reason"].(string); v != "" {
		cur.JobTypeReason = &v
	}
	if att, ok := filled["attestation"].(map[string]any); ok {
		if v, _ := att["status"].(string); v != "" {
			cur.AttestationStatus = v
		}
		if v, _ := att["attestor"].(string); v != "" {
			cur.Attestor = &v
		}
	}
	if v, _ := filled["attestation_status"].(string); v != "" {
		cur.AttestationStatus = v
	}
	if v, _ := filled["attestor"].(string); v != "" {
		cur.Attestor = &v
	}
	if v, _ := filled["hub_org"].(string); v != "" {
		cur.HubOrg = &v
	}
	if v, _ := filled["harbor_version"].(string); v != "" {
		cur.HarborVersion = &v
	}
	if v, _ := filled["dataset_name"].(string); v != "" {
		cur.DatasetName = &v
	}
	if v, _ := filled["dataset_version"].(string); v != "" {
		cur.DatasetVersion = &v
	}
	if v, exists := filled["dataset_ref"]; exists {
		cur.DatasetRef = optString(v)
	}
	if v, exists := filled["dataset_path"]; exists {
		cur.DatasetPath = optString(v)
	}
	if v, exists := filled["incomparability"]; exists {
		cur.Incomparability = optString(v)
	}
	if v, exists := filled["listed"]; exists {
		cur.Listed = asBool(v)
	}
	if v, exists := filled["published"]; exists {
		cur.Listed = asBool(v)
	}
	for k, v := range filled {
		if _, ok := known[k]; ok {
			continue
		}
		if strings.HasPrefix(k, "n_") || k == "pass_at_1" || k == "cost_usd" {
			continue
		}
		extra[k] = v
	}
	b, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	cur.Extra = string(b)
	cur.JobID = jobID
	if cur.AttestationStatus == "" {
		cur.AttestationStatus = "unsigned"
	}
	err = s.Store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return store.UpdateOverlay(ctx, tx, *cur)
	})
	if err != nil {
		return nil, err
	}
	return s.Store.GetOverlay(ctx, jobID)
}

func OverlayResponse(o *store.Overlay) map[string]any {
	if o == nil {
		return nil
	}
	var extra any
	if json.Unmarshal([]byte(o.Extra), &extra) != nil {
		extra = map[string]any{}
	}
	return map[string]any{
		"job_id":             o.JobID,
		"runner_name":        o.RunnerName,
		"runner_version":     o.RunnerVersion,
		"sandbox_type":       o.SandboxType,
		"sandbox_location":   o.SandboxLocation,
		"endpoint_class":     o.EndpointClass,
		"job_type":           o.JobType,
		"job_type_reason":    o.JobTypeReason,
		"attestation_status": o.AttestationStatus,
		"attestor":           o.Attestor,
		"hub_org":            o.HubOrg,
		"harbor_version":     o.HarborVersion,
		"dataset_name":       o.DatasetName,
		"dataset_version":    o.DatasetVersion,
		"dataset_ref":        o.DatasetRef,
		"dataset_path":       o.DatasetPath,
		"incomparability":    o.Incomparability,
		"listed":             o.Listed,
		"extra":              extra,
	}
}

func optString(v any) *string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return &t
	default:
		return nil
	}
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "true" || s == "1" || s == "yes"
	default:
		return false
	}
}

func CheckJobType(v any) error {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" || t == "H" || t == "M" {
			return nil
		}
		return fmt.Errorf("job_type must be H, M, or null")
	default:
		return fmt.Errorf("job_type must be H, M, or null")
	}
}
