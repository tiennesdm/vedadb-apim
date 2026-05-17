package publisher

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Lifecycle Implementation
// ============================================================================

// LifecycleState represents an API lifecycle state
type LifecycleState string

const (
	StateDraft      LifecycleState = "draft"
	StateReview     LifecycleState = "review"
	StatePublished  LifecycleState = "published"
	StateDeprecated LifecycleState = "deprecated"
	StateRetired    LifecycleState = "retired"
	StateRejected   LifecycleState = "rejected"
)

// Valid returns if the state is a valid lifecycle state
func (s LifecycleState) Valid() bool {
	switch s {
	case StateDraft, StateReview, StatePublished, StateDeprecated, StateRetired, StateRejected:
		return true
	}
	return false
}

// Role represents a user role
type Role string

const (
	RolePublisher    Role = "publisher"
	RoleReviewer     Role = "reviewer"
	RoleAdmin        Role = "admin"
	RoleSubscriber   Role = "subscriber"
	RoleAnonymous    Role = "anonymous"
)

// CanTransition checks if a role can initiate a transition
func (r Role) CanTransition(from, to LifecycleState) bool {
	// Define allowed transitions per role
	transitionRules := map[Role]map[LifecycleState][]LifecycleState{
		RolePublisher: {
			StateDraft:     {StateReview, StateDeprecated},
			StateReview:    {StateDraft}, // Can withdraw from review
			StatePublished: {StateDeprecated},
			StateRejected:  {StateDraft},
		},
		RoleReviewer: {
			StateReview: {StatePublished, StateRejected},
		},
		RoleAdmin: {
			StateDraft:      {StateReview, StateDeprecated, StatePublished},
			StateReview:     {StatePublished, StateRejected, StateDraft},
			StatePublished:  {StateDeprecated, StateRetired},
			StateDeprecated: {StateRetired, StatePublished},
			StateRejected:   {StateDraft, StateRetired},
			StateRetired:    {},
		},
		RoleSubscriber: {}, // Subscribers cannot transition
		RoleAnonymous:  {}, // Anonymous cannot transition
	}

	allowed, ok := transitionRules[r][from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// LifecycleManager manages API lifecycle transitions
type LifecycleManager struct {
	transitions []Transition
}

// Transition represents a lifecycle transition
type Transition struct {
	APIID     string
	From      LifecycleState
	To        LifecycleState
	By        Role
	Timestamp int64
	Reason    string
}

// NewLifecycleManager creates a new lifecycle manager
func NewLifecycleManager() *LifecycleManager {
	return &LifecycleManager{
		transitions: make([]Transition, 0),
	}
}

// Transition attempts to transition an API from one state to another
func (lm *LifecycleManager) Transition(apiID string, from, to LifecycleState, by Role, reason string) (*Transition, error) {
	if !from.Valid() {
		return nil, fmt.Errorf("invalid source state: %s", from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("invalid target state: %s", to)
	}
	if from == to {
		return nil, fmt.Errorf("cannot transition to the same state")
	}
	if !by.CanTransition(from, to) {
		return nil, fmt.Errorf("role %s cannot transition from %s to %s", by, from, to)
	}

	transition := Transition{
		APIID:  apiID,
		From:   from,
		To:     to,
		By:     by,
		Reason: reason,
	}
	lm.transitions = append(lm.transitions, transition)
	return &transition, nil
}

// GetTransitions returns all transitions for an API
func (lm *LifecycleManager) GetTransitions(apiID string) []Transition {
	var result []Transition
	for _, t := range lm.transitions {
		if t.APIID == apiID {
			result = append(result, t)
		}
	}
	return result
}

// GetAllowedTransitions returns allowed next states for a given state and role
func GetAllowedTransitions(state LifecycleState, role Role) []LifecycleState {
	allStates := []LifecycleState{
		StateDraft, StateReview, StatePublished,
		StateDeprecated, StateRetired, StateRejected,
	}

	var result []LifecycleState
	for _, s := range allStates {
		if s != state && role.CanTransition(state, s) {
			result = append(result, s)
		}
	}
	return result
}

// IsTerminalState checks if a state is terminal (no further transitions allowed)
func IsTerminalState(state LifecycleState) bool {
	return state == StateRetired
}

// ============================================================================
// TESTS
// ============================================================================

func TestLifecycleState_Valid_GivenVariousStates_WhenChecked_ThenCorrectResult(t *testing.T) {
	tests := []struct {
		name     string
		state    LifecycleState
		expected bool
	}{
		{"draft is valid", StateDraft, true},
		{"review is valid", StateReview, true},
		{"published is valid", StatePublished, true},
		{"deprecated is valid", StateDeprecated, true},
		{"retired is valid", StateRetired, true},
		{"rejected is valid", StateRejected, true},
		{"empty is invalid", "", false},
		{"unknown is invalid", "unknown", false},
		{"random is invalid", "random_state", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.Valid())
		})
	}
}

func TestRole_CanTransition_GivenPublisherRole_WhenChecking_ThenCorrectPermissions(t *testing.T) {
	tests := []struct {
		name     string
		from     LifecycleState
		to       LifecycleState
		expected bool
	}{
		{"draft to review", StateDraft, StateReview, true},
		{"draft to deprecated", StateDraft, StateDeprecated, true},
		{"draft to published", StateDraft, StatePublished, false},
		{"review to published", StateReview, StatePublished, false},
		{"review to draft", StateReview, StateDraft, true},
		{"review to rejected", StateReview, StateRejected, false},
		{"published to deprecated", StatePublished, StateDeprecated, true},
		{"published to retired", StatePublished, StateRetired, false},
		{"published to draft", StatePublished, StateDraft, false},
		{"rejected to draft", StateRejected, StateDraft, true},
		{"rejected to review", StateRejected, StateReview, false},
		{"deprecated to retired", StateDeprecated, StateRetired, false},
		{"deprecated to published", StateDeprecated, StatePublished, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RolePublisher.CanTransition(tt.from, tt.to)
			assert.Equal(t, tt.expected, result, "publisher should %sbe able to transition from %s to %s",
				map[bool]string{true: "", false: "not "}[tt.expected], tt.from, tt.to)
		})
	}
}

func TestRole_CanTransition_GivenReviewerRole_WhenChecking_ThenCorrectPermissions(t *testing.T) {
	tests := []struct {
		name     string
		from     LifecycleState
		to       LifecycleState
		expected bool
	}{
		{"review to published", StateReview, StatePublished, true},
		{"review to rejected", StateReview, StateRejected, true},
		{"review to draft", StateReview, StateDraft, false},
		{"draft to review", StateDraft, StateReview, false},
		{"published to deprecated", StatePublished, StateDeprecated, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RoleReviewer.CanTransition(tt.from, tt.to)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRole_CanTransition_GivenAdminRole_WhenChecking_ThenAllTransitionsAllowed(t *testing.T) {
	tests := []struct {
		name     string
		from     LifecycleState
		to       LifecycleState
		expected bool
	}{
		{"draft to review", StateDraft, StateReview, true},
		{"draft to deprecated", StateDraft, StateDeprecated, true},
		{"draft to published", StateDraft, StatePublished, true},
		{"review to published", StateReview, StatePublished, true},
		{"review to rejected", StateReview, StateRejected, true},
		{"review to draft", StateReview, StateDraft, true},
		{"published to deprecated", StatePublished, StateDeprecated, true},
		{"published to retired", StatePublished, StateRetired, true},
		{"deprecated to retired", StateDeprecated, StateRetired, true},
		{"deprecated to published", StateDeprecated, StatePublished, true},
		{"rejected to draft", StateRejected, StateDraft, true},
		{"rejected to retired", StateRejected, StateRetired, true},
		{"retired to anything", StateRetired, StateDraft, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RoleAdmin.CanTransition(tt.from, tt.to)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRole_CanTransition_GivenSubscriberAndAnonymous_WhenChecking_ThenNoPermissions(t *testing.T) {
	states := []LifecycleState{StateDraft, StateReview, StatePublished, StateDeprecated, StateRetired, StateRejected}
	roles := []Role{RoleSubscriber, RoleAnonymous}

	for _, role := range roles {
		for _, from := range states {
			for _, to := range states {
				if from != to {
					t.Run(fmt.Sprintf("%s_%s_to_%s", role, from, to), func(t *testing.T) {
						assert.False(t, role.CanTransition(from, to),
							"%s should not be able to transition from %s to %s", role, from, to)
					})
				}
			}
		}
	}
}

func TestLifecycleManager_Transition_GivenValidTransition_WhenTransitioned_ThenSucceeds(t *testing.T) {
	lm := NewLifecycleManager()

	tests := []struct {
		name   string
		from   LifecycleState
		to     LifecycleState
		role   Role
		reason string
	}{
		{"publisher draft to review", StateDraft, StateReview, RolePublisher, "Ready for review"},
		{"reviewer review to publish", StateReview, StatePublished, RoleReviewer, "Approved"},
		{"reviewer review to reject", StateReview, StateRejected, RoleReviewer, "Needs changes"},
		{"admin draft to publish", StateDraft, StatePublished, RoleAdmin, "Emergency publish"},
		{"admin published to retired", StatePublished, StateRetired, RoleAdmin, "End of life"},
		{"publisher rejected to draft", StateRejected, StateDraft, RolePublisher, "Addressed feedback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transition, err := lm.Transition("api-1", tt.from, tt.to, tt.role, tt.reason)
			require.NoError(t, err)
			assert.NotNil(t, transition)
			assert.Equal(t, "api-1", transition.APIID)
			assert.Equal(t, tt.from, transition.From)
			assert.Equal(t, tt.to, transition.To)
			assert.Equal(t, tt.role, transition.By)
			assert.Equal(t, tt.reason, transition.Reason)
		})
	}
}

func TestLifecycleManager_Transition_GivenInvalidTransition_WhenTransitioned_ThenReturnsError(t *testing.T) {
	lm := NewLifecycleManager()

	tests := []struct {
		name   string
		from   LifecycleState
		to     LifecycleState
		role   Role
		errMsg string
	}{
		{"publisher cannot publish", StateDraft, StatePublished, RolePublisher, "cannot transition"},
		{"publisher cannot retire", StatePublished, StateRetired, RolePublisher, "cannot transition"},
		{"reviewer cannot draft to review", StateDraft, StateReview, RoleReviewer, "cannot transition"},
		{"subscriber cannot do anything", StateDraft, StateReview, RoleSubscriber, "cannot transition"},
		{"anonymous cannot do anything", StateDraft, StateReview, RoleAnonymous, "cannot transition"},
		{"retired is terminal", StateRetired, StateDraft, RoleAdmin, "cannot transition"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transition, err := lm.Transition("api-1", tt.from, tt.to, tt.role, "test")
			assert.Error(t, err)
			assert.Nil(t, transition)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestLifecycleManager_Transition_GivenInvalidStates_WhenTransitioned_ThenReturnsError(t *testing.T) {
	lm := NewLifecycleManager()

	tests := []struct {
		name   string
		from   LifecycleState
		to     LifecycleState
		errMsg string
	}{
		{"invalid source state", "invalid", StateReview, "invalid source state"},
		{"invalid target state", StateDraft, "invalid", "invalid target state"},
		{"both invalid", "bad", "worse", "invalid source state"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transition, err := lm.Transition("api-1", tt.from, tt.to, RoleAdmin, "test")
			assert.Error(t, err)
			assert.Nil(t, transition)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestLifecycleManager_Transition_GivenSameState_WhenTransitioned_ThenReturnsError(t *testing.T) {
	lm := NewLifecycleManager()
	transition, err := lm.Transition("api-1", StateDraft, StateDraft, RoleAdmin, "test")
	assert.Error(t, err)
	assert.Nil(t, transition)
	assert.Contains(t, err.Error(), "same state")
}

func TestLifecycleManager_GetTransitions_GivenMultipleTransitions_WhenQueried_ThenReturnsHistory(t *testing.T) {
	lm := NewLifecycleManager()

	// Multiple transitions for api-1
	lm.Transition("api-1", StateDraft, StateReview, RolePublisher, "Ready for review")
	lm.Transition("api-1", StateReview, StatePublished, RoleReviewer, "Looks good")
	lm.Transition("api-1", StatePublished, StateDeprecated, RolePublisher, "New version available")

	// Transition for api-2
	lm.Transition("api-2", StateDraft, StateReview, RolePublisher, "Ready")

	// Get api-1 transitions
	transitions := lm.GetTransitions("api-1")
	require.Len(t, transitions, 3)
	assert.Equal(t, StateDraft, transitions[0].From)
	assert.Equal(t, StateReview, transitions[0].To)
	assert.Equal(t, StateReview, transitions[1].From)
	assert.Equal(t, StatePublished, transitions[1].To)
	assert.Equal(t, StatePublished, transitions[2].From)
	assert.Equal(t, StateDeprecated, transitions[2].To)

	// Get api-2 transitions
	transitions = lm.GetTransitions("api-2")
	require.Len(t, transitions, 1)
	assert.Equal(t, StateDraft, transitions[0].From)
	assert.Equal(t, StateReview, transitions[0].To)

	// Get non-existent API transitions
	transitions = lm.GetTransitions("api-999")
	assert.Len(t, transitions, 0)
}

func TestGetAllowedTransitions_GivenVariousStatesAndRoles_WhenQueried_ThenReturnsCorrectList(t *testing.T) {
	tests := []struct {
		name     string
		state    LifecycleState
		role     Role
		expected []LifecycleState
	}{
		{
			name:     "admin from draft",
			state:    StateDraft,
			role:     RoleAdmin,
			expected: []LifecycleState{StateReview, StateDeprecated, StatePublished},
		},
		{
			name:     "publisher from draft",
			state:    StateDraft,
			role:     RolePublisher,
			expected: []LifecycleState{StateReview, StateDeprecated},
		},
		{
			name:     "reviewer from review",
			state:    StateReview,
			role:     RoleReviewer,
			expected: []LifecycleState{StatePublished, StateRejected},
		},
		{
			name:     "publisher from published",
			state:    StatePublished,
			role:     RolePublisher,
			expected: []LifecycleState{StateDeprecated},
		},
		{
			name:     "admin from retired",
			state:    StateRetired,
			role:     RoleAdmin,
			expected: []LifecycleState{},
		},
		{
			name:     "subscriber from draft",
			state:    StateDraft,
			role:     RoleSubscriber,
			expected: []LifecycleState{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAllowedTransitions(tt.state, tt.role)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsTerminalState_GivenVariousStates_WhenChecked_ThenCorrectResult(t *testing.T) {
	tests := []struct {
		state    LifecycleState
		expected bool
	}{
		{StateDraft, false},
		{StateReview, false},
		{StatePublished, false},
		{StateDeprecated, false},
		{StateRetired, true},
		{StateRejected, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			assert.Equal(t, tt.expected, IsTerminalState(tt.state))
		})
	}
}

func TestLifecycleManager_CompleteWorkflow_GivenRealisticScenario_WhenAllTransitions_ThenSuccess(t *testing.T) {
	lm := NewLifecycleManager()

	// Step 1: Publisher creates API (draft)
	// Step 2: Publisher submits for review
	_, err := lm.Transition("payment-api", StateDraft, StateReview, RolePublisher, "Initial submission")
	require.NoError(t, err)

	// Step 3: Reviewer approves and publishes
	_, err = lm.Transition("payment-api", StateReview, StatePublished, RoleReviewer, "Code review passed, docs complete")
	require.NoError(t, err)

	// Step 4: Publisher deprecates when v2 is released
	_, err = lm.Transition("payment-api", StatePublished, StateDeprecated, RolePublisher, "v2.0.0 is now available")
	require.NoError(t, err)

	// Step 5: Admin retires after deprecation period
	_, err = lm.Transition("payment-api", StateDeprecated, StateRetired, RoleAdmin, "6-month deprecation period ended")
	require.NoError(t, err)

	// Verify full history
	transitions := lm.GetTransitions("payment-api")
	require.Len(t, transitions, 4)
	assert.Equal(t, StateDraft, transitions[0].From)
	assert.Equal(t, StateReview, transitions[0].To)
	assert.Equal(t, RolePublisher, transitions[0].By)
	assert.Equal(t, StateReview, transitions[1].From)
	assert.Equal(t, StatePublished, transitions[1].To)
	assert.Equal(t, RoleReviewer, transitions[1].By)
	assert.Equal(t, StatePublished, transitions[2].From)
	assert.Equal(t, StateDeprecated, transitions[2].To)
	assert.Equal(t, StateDeprecated, transitions[3].From)
	assert.Equal(t, StateRetired, transitions[3].To)
}

func TestLifecycleManager_RejectedWorkflow_GivenRejectedAPI_WhenResubmitted_ThenSuccess(t *testing.T) {
	lm := NewLifecycleManager()

	// Submit
	_, err := lm.Transition("api-1", StateDraft, StateReview, RolePublisher, "First submission")
	require.NoError(t, err)

	// Rejected
	_, err = lm.Transition("api-1", StateReview, StateRejected, RoleReviewer, "Missing documentation")
	require.NoError(t, err)

	// Back to draft
	_, err = lm.Transition("api-1", StateRejected, StateDraft, RolePublisher, "Added documentation")
	require.NoError(t, err)

	// Resubmit
	_, err = lm.Transition("api-1", StateDraft, StateReview, RolePublisher, "Resubmission")
	require.NoError(t, err)

	// Published
	_, err = lm.Transition("api-1", StateReview, StatePublished, RoleReviewer, "Approved")
	require.NoError(t, err)

	transitions := lm.GetTransitions("api-1")
	require.Len(t, transitions, 5)
}

func TestRole_String_GivenRole_WhenStringed_ThenReturnsCorrectValue(t *testing.T) {
	assert.Equal(t, "publisher", string(RolePublisher))
	assert.Equal(t, "reviewer", string(RoleReviewer))
	assert.Equal(t, "admin", string(RoleAdmin))
	assert.Equal(t, "subscriber", string(RoleSubscriber))
	assert.Equal(t, "anonymous", string(RoleAnonymous))
}
