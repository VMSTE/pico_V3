package pika

import "testing"

func TestCheckLoopDetection_NilTrail(t *testing.T) {
	if CheckLoopDetection(nil, 3) {
		t.Error("expected false for nil trail")
	}
}

func TestCheckLoopDetection_ZeroThreshold(t *testing.T) {
	tr := NewTrail()
	if CheckLoopDetection(tr, 0) {
		t.Error("expected false for zero threshold")
	}
}

func TestCheckLoopDetection_NegativeThreshold(t *testing.T) {
	tr := NewTrail()
	if CheckLoopDetection(tr, -1) {
		t.Error("expected false for negative threshold")
	}
}

func TestCheckLoopDetection_DelegatesToTrail(t *testing.T) {
	tr := NewTrail()
	// Empty trail should not detect loop
	if CheckLoopDetection(tr, 3) {
		t.Error("expected false for empty trail")
	}
}
