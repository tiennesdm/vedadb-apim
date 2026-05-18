package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vedadb/vapim/pkg/models"
)

// PolicyType defines the type of throttle policy.
type PolicyType string

const (
	PolicyTypeAdvanced      PolicyType = "Advanced"
	PolicyTypeSubscription  PolicyType = "Subscription"
	PolicyTypeApplication   PolicyType = "Application"
	PolicyTypeGlobal        PolicyType = "Global"
	PolicyTypeCustom        PolicyType = "Custom"
)

// PolicyAction defines what action to take when a policy matches.
type PolicyAction string

const (
	ActionBlock     PolicyAction = "BLOCK"
	ActionThrottle  PolicyAction = "THROTTLE"
	ActionLog       PolicyAction = "LOG"
	ActionAllow     PolicyAction = "ALLOW"
)

// ConditionType defines the type of condition to evaluate.
type ConditionType string

const (
	ConditionIP         ConditionType = "IP"
	ConditionHeader     ConditionType = "Header"
	ConditionQueryParam ConditionType = "QueryParam"
	ConditionJWTClaim   ConditionType = "JWTClaim"
	ConditionTime       ConditionType = "Time"
	ConditionUserAgent  ConditionType = "UserAgent"
)

// PolicyEngine evaluates throttle policies with conditions and actions.
type PolicyEngine struct {
	store    ThrottleStore
	logger   *slog.Logger
	mu       sync.RWMutex
	policies map[string]*CompiledPolicy // policyID -> compiled policy
	pipeline []PolicyPipelineStage
}

// CompiledPolicy is a policy that has been parsed and compiled for fast evaluation.
type CompiledPolicy struct {
	ID          string
	Name        string
	Type        PolicyType
	Priority    int
	Enabled     bool
	Conditions  []CompiledCondition
	Action      PolicyAction
	Limit       int64
	TimeWindow  time.Duration
	Tier        string // applicable tier, empty means all
	CompiledAt  time.Time
}

// CompiledCondition is a pre-compiled condition for fast evaluation.
type CompiledCondition struct {
	Type       ConditionType
	Field      string
	Operator   string
	Value      string
	Values     []string // for IN operator
	Regex      *regexp.Regexp
	IPNet      *net.IPNet // for IP range conditions
}

// PolicyPipelineStage represents a stage in the policy evaluation pipeline.
type PolicyPipelineStage struct {
	Name     string
	Evaluate func(ctx context.Context, req *models.ThrottleCheckRequest, policy *CompiledPolicy) (bool, PolicyAction)
}

// PolicyResult is the outcome of policy evaluation.
type PolicyResult struct {
	Matched    bool
	Action     PolicyAction
	PolicyID   string
	PolicyName string
	Limit      int64
	Window     time.Duration
	Reason     string
}

// NewPolicyEngine creates a new policy engine.
func NewPolicyEngine(store ThrottleStore, logger *slog.Logger) *PolicyEngine {
	if logger == nil {
		logger = slog.Default()
	}

	pe := &PolicyEngine{
		store:    store,
		logger:   logger.With("component", "policy-engine"),
		policies: make(map[string]*CompiledPolicy),
	}

	// Build the evaluation pipeline
	pe.buildPipeline()

	// Start policy refresh loop
	go pe.refreshLoop()

	return pe
}

// buildPipeline constructs the policy evaluation pipeline stages.
func (pe *PolicyEngine) buildPipeline() {
	pe.pipeline = []PolicyPipelineStage{
		{"condition_eval", pe.evaluateConditions},
		{"tier_match", pe.evaluateTierMatch},
		{"time_window", pe.evaluateTimeWindow},
	}
}

// Evaluate runs the full policy evaluation pipeline against a request.
func (pe *PolicyEngine) Evaluate(ctx context.Context, req *models.ThrottleCheckRequest) *PolicyResult {
	pe.mu.RLock()
	policies := make([]*CompiledPolicy, 0, len(pe.policies))
	for _, p := range pe.policies {
		if p.Enabled {
			policies = append(policies, p)
		}
	}
	pe.mu.RUnlock()

	// Sort by priority (highest first)
	sortPoliciesByPriority(policies)

	for _, policy := range policies {
		result := pe.evaluatePolicy(ctx, req, policy)
		if result.Matched {
			return result
		}
	}

	// No policy matched - allow by default
	return &PolicyResult{
		Matched: false,
		Action:  ActionAllow,
	}
}

func (pe *PolicyEngine) evaluatePolicy(ctx context.Context, req *models.ThrottleCheckRequest, policy *CompiledPolicy) *PolicyResult {
	allConditionsMatch := true

	for _, stage := range pe.pipeline {
		matched, action := stage.Evaluate(ctx, req, policy)
		if !matched {
			allConditionsMatch = false
			break
		}
		if action == ActionBlock || action == ActionThrottle {
			return &PolicyResult{
				Matched:    true,
				Action:     action,
				PolicyID:   policy.ID,
				PolicyName: policy.Name,
				Limit:      policy.Limit,
				Window:     policy.TimeWindow,
				Reason:     fmt.Sprintf("pipeline stage '%s' matched", stage.Name),
			}
		}
	}

	if allConditionsMatch {
		return &PolicyResult{
			Matched:    true,
			Action:     policy.Action,
			PolicyID:   policy.ID,
			PolicyName: policy.Name,
			Limit:      policy.Limit,
			Window:     policy.TimeWindow,
			Reason:     "all conditions matched",
		}
	}

	return &PolicyResult{Matched: false}
}

// --- Pipeline Stages ---

func (pe *PolicyEngine) evaluateConditions(ctx context.Context, req *models.ThrottleCheckRequest, policy *CompiledPolicy) (bool, PolicyAction) {
	if len(policy.Conditions) == 0 {
		return true, policy.Action // no conditions means always match
	}

	for _, cond := range policy.Conditions {
		matched := pe.evaluateCondition(cond, req)
		if !matched {
			return false, ActionAllow
		}
	}

	return true, policy.Action
}

func (pe *PolicyEngine) evaluateTierMatch(ctx context.Context, req *models.ThrottleCheckRequest, policy *CompiledPolicy) (bool, PolicyAction) {
	if policy.Tier == "" {
		return true, policy.Action // no tier restriction
	}
	return policy.Tier == req.Tier, policy.Action
}

func (pe *PolicyEngine) evaluateTimeWindow(ctx context.Context, req *models.ThrottleCheckRequest, policy *CompiledPolicy) (bool, PolicyAction) {
	if policy.TimeWindow == 0 {
		return true, policy.Action // no time window restriction
	}

	// Time window is evaluated by the counter mechanism
	return true, policy.Action
}

// --- Condition Evaluation ---

func (pe *PolicyEngine) evaluateCondition(cond CompiledCondition, req *models.ThrottleCheckRequest) bool {
	switch cond.Type {
	case ConditionIP:
		return pe.evaluateIPCondition(cond, req)
	case ConditionHeader:
		return pe.evaluateHeaderCondition(cond, req)
	case ConditionQueryParam:
		return pe.evaluateQueryParamCondition(cond, req)
	case ConditionJWTClaim:
		return pe.evaluateJWTClaimCondition(cond, req)
	case ConditionTime:
		return pe.evaluateTimeCondition(cond)
	case ConditionUserAgent:
		return pe.evaluateUserAgentCondition(cond, req)
	default:
		return false
	}
}

func (pe *PolicyEngine) evaluateIPCondition(cond CompiledCondition, req *models.ThrottleCheckRequest) bool {
	if req.ClientIP == "" {
		return false
	}

	clientIP := net.ParseIP(req.ClientIP)
	if clientIP == nil {
		return false
	}

	switch cond.Operator {
	case "eq":
		return req.ClientIP == cond.Value
	case "ne":
		return req.ClientIP != cond.Value
	case "in":
		for _, v := range cond.Values {
			if req.ClientIP == v {
				return true
			}
		}
		return false
	case "range":
		if cond.IPNet != nil {
			return cond.IPNet.Contains(clientIP)
		}
		return false
	default:
		return false
	}
}

func (pe *PolicyEngine) evaluateHeaderCondition(cond CompiledCondition, req *models.ThrottleCheckRequest) bool {
	headerValue := ""
	if req.Headers != nil {
		headerValue = req.Headers[cond.Field]
	}

	return matchStringValue(headerValue, cond.Value, cond.Values, cond.Regex, cond.Operator)
}

func (pe *PolicyEngine) evaluateQueryParamCondition(cond CompiledCondition, req *models.ThrottleCheckRequest) bool {
	paramValue := ""
	if req.QueryParams != nil {
		paramValue = req.QueryParams[cond.Field]
	}

	return matchStringValue(paramValue, cond.Value, cond.Values, cond.Regex, cond.Operator)
}

func (pe *PolicyEngine) evaluateJWTClaimCondition(cond CompiledCondition, req *models.ThrottleCheckRequest) bool {
	claimValue := ""
	if req.JWTClaims != nil {
		if v, ok := req.JWTClaims[cond.Field]; ok {
			claimValue = fmt.Sprintf("%v", v)
		}
	}

	return matchStringValue(claimValue, cond.Value, cond.Values, cond.Regex, cond.Operator)
}

func (pe *PolicyEngine) evaluateTimeCondition(cond CompiledCondition) bool {
	now := time.Now()

	switch cond.Field {
	case "hour":
		return matchIntRange(int64(now.Hour()), cond.Value, cond.Operator)
	case "day_of_week":
		return matchIntRange(int64(now.Weekday()), cond.Value, cond.Operator)
	case "day_of_month":
		return matchIntRange(int64(now.Day()), cond.Value, cond.Operator)
	default:
		return false
	}
}

func (pe *PolicyEngine) evaluateUserAgentCondition(cond CompiledCondition, req *models.ThrottleCheckRequest) bool {
	ua := ""
	if req.Headers != nil {
		ua = req.Headers["User-Agent"]
	}

	return matchStringValue(ua, cond.Value, cond.Values, cond.Regex, cond.Operator)
}

// --- Policy Compilation ---

// CompilePolicy compiles a raw policy into an optimized form for fast evaluation.
func (pe *PolicyEngine) CompilePolicy(policy *models.ThrottlePolicy) (*CompiledPolicy, error) {
	compiled := &CompiledPolicy{
		ID:         policy.ID,
		Name:       policy.Name,
		Type:       PolicyType(policy.Type),
		Priority:   policy.Priority,
		Enabled:    policy.Enabled,
		Action:     PolicyAction(policy.Action),
		Limit:      policy.Limit,
		TimeWindow: policy.TimeWindow,
		Tier:       policy.Tier,
		CompiledAt: time.Now().UTC(),
	}

	// Compile conditions
	for _, cond := range policy.Conditions {
		compiledCond, err := pe.compileCondition(&cond)
		if err != nil {
			return nil, fmt.Errorf("failed to compile condition for policy %s: %w", policy.ID, err)
		}
		compiled.Conditions = append(compiled.Conditions, *compiledCond)
	}

	return compiled, nil
}

func (pe *PolicyEngine) compileCondition(cond *models.PolicyCondition) (*CompiledCondition, error) {
	compiled := &CompiledCondition{
		Type:     ConditionType(cond.Type),
		Field:    cond.Field,
		Operator: cond.Operator,
		Value:    cond.Value,
	}

	// Pre-compile regex for pattern matching
	if cond.Operator == "regex" || cond.Operator == "match" {
		re, err := regexp.Compile(cond.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %w", err)
		}
		compiled.Regex = re
	}

	// Pre-compile IP network for range matching
	if cond.Type == string(ConditionIP) && cond.Operator == "range" {
		_, ipNet, err := net.ParseCIDR(cond.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR range: %w", err)
		}
		compiled.IPNet = ipNet
	}

	// Pre-parse multi-value conditions
	if cond.Operator == "in" && cond.Values != nil {
		compiled.Values = cond.Values
	}

	return compiled, nil
}

// --- Policy Management ---

// LoadPolicy loads and compiles a policy by ID.
func (pe *PolicyEngine) LoadPolicy(ctx context.Context, policyID string) error {
	policy, err := pe.store.GetPolicy(ctx, policyID)
	if err != nil {
		return fmt.Errorf("failed to load policy %s: %w", policyID, err)
	}

	compiled, err := pe.CompilePolicy(policy)
	if err != nil {
		return err
	}

	pe.mu.Lock()
	pe.policies[policyID] = compiled
	pe.mu.Unlock()

	pe.logger.Info("policy loaded", "policy_id", policyID, "name", policy.Name, "type", policy.Type)
	return nil
}

// UnloadPolicy removes a policy from the engine.
func (pe *PolicyEngine) UnloadPolicy(policyID string) {
	pe.mu.Lock()
	delete(pe.policies, policyID)
	pe.mu.Unlock()

	pe.logger.Info("policy unloaded", "policy_id", policyID)
}

// ReloadAllPolicies reloads all policies from the store.
func (pe *PolicyEngine) ReloadAllPolicies(ctx context.Context) error {
	policies, err := pe.store.ListPolicies(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to list policies: %w", err)
	}

	newPolicies := make(map[string]*CompiledPolicy)
	for _, policy := range policies {
		compiled, err := pe.CompilePolicy(&policy)
		if err != nil {
			pe.logger.Warn("failed to compile policy, skipping", "policy_id", policy.ID, "error", err)
			continue
		}
		newPolicies[policy.ID] = compiled
	}

	pe.mu.Lock()
	pe.policies = newPolicies
	pe.mu.Unlock()

	pe.logger.Info("all policies reloaded", "count", len(newPolicies))
	return nil
}

// ListActivePolicies returns all currently active policies.
func (pe *PolicyEngine) ListActivePolicies() []*CompiledPolicy {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	result := make([]*CompiledPolicy, 0, len(pe.policies))
	for _, p := range pe.policies {
		if p.Enabled {
			result = append(result, p)
		}
	}
	return result
}

// refreshLoop periodically refreshes policies from the store.
func (pe *PolicyEngine) refreshLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := pe.ReloadAllPolicies(ctx); err != nil {
			pe.logger.Warn("policy refresh failed", "error", err)
		}
		cancel()
	}
}

// --- Helpers ---

func matchStringValue(value, pattern string, values []string, regex *regexp.Regexp, operator string) bool {
	switch operator {
	case "eq":
		return value == pattern
	case "ne":
		return value != pattern
	case "in":
		for _, v := range values {
			if value == v {
				return true
			}
		}
		return false
	case "contains":
		return strings.Contains(value, pattern)
	case "startswith":
		return strings.HasPrefix(value, pattern)
	case "endswith":
		return strings.HasSuffix(value, pattern)
	case "regex", "match":
		if regex != nil {
			return regex.MatchString(value)
		}
		return false
	case "exists":
		return value != ""
	case "not_exists":
		return value == ""
	default:
		return false
	}
}

func matchIntRange(value int64, pattern string, operator string) bool {
	var target int64
	fmt.Sscanf(pattern, "%d", &target)

	switch operator {
	case "eq":
		return value == target
	case "ne":
		return value != target
	case "lt":
		return value < target
	case "lte":
		return value <= target
	case "gt":
		return value > target
	case "gte":
		return value >= target
	default:
		return false
	}
}

func sortPoliciesByPriority(policies []*CompiledPolicy) {
	// Simple bubble sort by priority (descending)
	for i := 0; i < len(policies); i++ {
		for j := i + 1; j < len(policies); j++ {
			if policies[j].Priority > policies[i].Priority {
				policies[i], policies[j] = policies[j], policies[i]
			}
		}
	}
}
