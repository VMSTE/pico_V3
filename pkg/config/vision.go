package config

// SupportsVision reports whether the model accepts image input.
// nil (unset) is treated as false: unknown capability routes to the
// vision satellite instead of risking blind answers. (D-AUDIT-124)
func (mc *ModelConfig) SupportsVision() bool {
	return mc != nil && mc.Vision != nil && *mc.Vision
}
