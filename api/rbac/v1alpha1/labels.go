package v1alpha1

const (
	// LabelKeyServiceAccount can be used to mark a Kubernetes ServiceAccount as a
	// also being a Kargo ServiceAccount.
	LabelKeyServiceAccount = "kargo.akuity.io/service-account"

	// LabelValueTrue is used to identify a label that has a value of "true".
	LabelValueTrue = "true"
)
