package weather

import "github.com/choigonyok/jarvis/gocommon/weather"

// The shapes are shared with the adapter that reads the forecast
// (context-svc); what is here is how the loop asks for them.
type (
	Point    = weather.Point
	Day      = weather.Day
	Hour     = weather.Hour
	Forecast = weather.Forecast
	Source   = weather.Source
)

var (
	// ErrUnknown is a place no map knows by that name.
	ErrUnknown = weather.ErrUnknown
	// Names are the names worth looking a place up by, best first.
	Names = weather.Names
	// From is when the rain starts within the hours given.
	From = weather.From
	// Sky is the WMO code in a word or two.
	Sky = weather.Sky
	// ParsePoint reads "37.5665,126.9780" or "37.5665,126.9780,서울".
	ParsePoint = weather.ParsePoint
)
