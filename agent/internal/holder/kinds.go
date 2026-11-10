package holder

// The cards a request the browser's guard stopped waits on (the proposal's
// Action.Kind). Both are raised in httpapi; the jobs module reads them too,
// to tell a job how its card was decided.
const (
	// KindPayment is a payment the guard stopped (httpapi/intercept.go).
	KindPayment = "browser.payment"
	// KindFlow asks whether a site joins the payment flow: a page of a job's
	// run the guard stopped on its way out of the job's sites while the job
	// was paying, or to an address that reads as a payment's
	// (httpapi/leave.go). Approved, every job's page may go there.
	KindFlow = "browser.flow"
)
