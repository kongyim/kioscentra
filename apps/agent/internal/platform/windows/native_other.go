//go:build !windows

package windows

import (
	"context"
	"kiosentra/packages/contracts"
	"time"
)

func Software(context.Context) ([]contracts.Software, []contracts.Software, error) {
	return []contracts.Software{}, []contracts.Software{}, ErrUnsupported
}
func FileVersion(context.Context, string) (string, error) { return "", ErrUnsupported }
func Policy(_ context.Context, p contracts.Policy) contracts.PolicyReport {
	return contracts.PolicyReport{Revision: p.Revision, ObservedAt: time.Now().UTC(), Compliance: "unknown", Drift: []string{}, Errors: []string{ErrUnsupported.Error()}}
}
