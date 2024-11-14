package v1alpha1

// Name is a helper function to get the name of the backend from the LLMBackend.
func (l *LLMBackend) Name() string {
	return string(l.BackendRef.Name)
}
