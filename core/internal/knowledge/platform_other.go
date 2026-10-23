//go:build !darwin

package knowledge

// platformReason is "" where seekdb's runtime has no version floor to check.
func platformReason() string { return "" }
