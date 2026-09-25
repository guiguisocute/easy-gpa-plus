package api

import "testing"

func TestDispatchOptionsAlwaysAvoidSelf(t *testing.T) {
	unsafe := false
	seed, avoidSelf := dispatchOptions(dispatchInput{Seed: 42, AvoidSelf: &unsafe})
	if seed != 42 || !avoidSelf {
		t.Fatalf("dispatch options = seed %d, avoidSelf %v", seed, avoidSelf)
	}
}
