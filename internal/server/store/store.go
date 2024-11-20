package store

import (
	"github.com/cedar-policy/cedar-go"
	cedartypes "github.com/cedar-policy/cedar-go/types"
)

type PolicyStore interface {
	// InitalPolicyLoadComplete signals if authorizer is ready to authorize requests
	// While this is false, the authorizer will emit an authorizer.NoOpinion until its ready
	InitalPolicyLoadComplete() bool
	PolicySet() *cedar.PolicySet
	Name() string
}

type TieredPolicyStores []PolicyStore

// IsAuthorized returns looks for an explicit decision in each policy store, first to last.
// If there is no explicit decision, it checks the subsequent policy store. If no explicit
// policies are identified in the last store, that store's decision (forbid) is returned.
func (s TieredPolicyStores) IsAuthorized(entities cedartypes.EntityMap, req cedar.Request) (cedar.Decision, cedar.Diagnostic) {
	var (
		decision   cedar.Decision
		diagnostic cedar.Diagnostic
	)
	for i, store := range s {
		decision, diagnostic = store.PolicySet().IsAuthorized(entities, req)
		if len(s)-1 == i {
			break
		}

		if decision == cedar.Deny && len(diagnostic.Reasons) == 0 && len(diagnostic.Errors) == 0 {
			continue
		}
		break
	}
	return decision, diagnostic
}
