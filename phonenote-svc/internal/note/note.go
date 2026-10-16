// Package note is a phone notification, as the collector hands it over and
// as this service comes to understand it.
package note

import "time"

// In is one notification as a collector read it from a Mac's notification
// store (iPhone Mirroring forwards the phone's there).
type In struct {
	// Key is the collector's own id for it, unique on that device: a
	// notification shown twice is stored once.
	Key string `json:"key"`
	// App is the bundle id: com.jawebs.baedal, com.kbstar.kbbank …
	App      string    `json:"app"`
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle"`
	Body     string    `json:"body"`
	At       time.Time `json:"at"`
}

// Kinds a reading can be. Anything else reads as Other.
const (
	DeliveryOrder = "delivery_order" // a food order placed, accepted, on its way, arrived
	Purchase      = "purchase"       // a payment or a shop's order
	Deposit       = "deposit"        // money in
	Withdrawal    = "withdrawal"     // money out: a transfer, a bill paid
	Shipping      = "shipping"       // a parcel sent, on its way, delivered
	Reservation   = "reservation"    // a booking made, changed, coming up
	Bill          = "bill"           // money owed: a statement, a due date
	Message       = "message"        // a person wrote
	Reminder      = "reminder"       // an app reminding of something set earlier
	Promotion     = "promotion"      // an ad, a coupon
	Security      = "security"       // a login, a code
	Other         = "other"
)

// Item is one thing in an order.
type Item struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity,omitempty"`
	PriceKrw int64  `json:"priceKrw,omitempty"`
	// Category is spending-svc's, for filing the item under a payment.
	Category string `json:"category,omitempty"`
}

// Reading is what a notification was understood to say.
type Reading struct {
	Kind string `json:"kind"`
	// Meaningful: worth keeping as something that happened. An ad, a code,
	// "새 기능이 나왔어요" are not.
	Meaningful bool   `json:"meaningful"`
	Summary    string `json:"summary,omitempty"`
	// Merchant is the shop or the restaurant; Items what was ordered.
	Merchant  string `json:"merchant,omitempty"`
	Items     []Item `json:"items,omitempty"`
	AmountKrw int64  `json:"amountKrw,omitempty"`
	// Counterparty is who money came from or went to.
	Counterparty string `json:"counterparty,omitempty"`
	// Salary: a deposit that is pay.
	Salary bool `json:"salary,omitempty"`
	// Stage is where an order or a parcel is: placed, accepted, shipped,
	// arriving, delivered, cancelled.
	Stage string `json:"stage,omitempty"`
	// By is what read it: "rule" or the model.
	By string `json:"by,omitempty"`
}

// Note is a notification stored, with what came of it.
type Note struct {
	ID         int64     `json:"id"`
	Device     string    `json:"device"`
	App        string    `json:"app"`
	AppName    string    `json:"appName"`
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle"`
	Body       string    `json:"body"`
	At         time.Time `json:"at"`
	ReceivedAt time.Time `json:"receivedAt"`
	// Status: new (not read yet), read (understood; may still be waiting to
	// apply), done, skipped (not worth reading), failed.
	Status  string   `json:"status"`
	Reading *Reading `json:"reading,omitempty"`
	// Applied says what changed elsewhere because of it: "가계부 #812 에 상품 2개".
	Applied string `json:"applied,omitempty"`
	Error   string `json:"error,omitempty"`
	Tries   int    `json:"tries"`
}

// Text is the notification as one block, as a person would read it.
func (n Note) Text() string {
	s := n.Title
	if n.Subtitle != "" {
		s += "\n" + n.Subtitle
	}
	if n.Body != "" {
		s += "\n" + n.Body
	}
	return s
}
