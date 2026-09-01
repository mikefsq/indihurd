package binding

import (
	"github.com/mikefsq/goalpaca/alpaca"
)

type notServing interface{ NotServingReason() string }

// NotConnected is 0x407 with the reason in the message.
func NotConnected(reason string) error {
	if reason == "" {
		reason = "hardware not available"
	}
	return &alpaca.AlpacaError{Number: alpaca.ErrNumNotConnected, Message: "INDI: " + reason}
}

// NotImplemented is 0x400 naming the member.
func NotImplemented(member string) error {
	return &alpaca.AlpacaError{Number: alpaca.ErrNumNotImplemented, Message: member + " is not implemented by this INDI driver"}
}

// InvalidValue is 0x401.
func InvalidValue(msg string) error {
	return &alpaca.AlpacaError{Number: alpaca.ErrNumInvalidValue, Message: msg}
}

// InvalidOperation is 0x40B.
func InvalidOperation(msg string) error {
	return &alpaca.AlpacaError{Number: alpaca.ErrNumInvalidOperation, Message: msg}
}

// DriverError is 0x500 with the driver's own message appended to context.
func DriverError(context, driverMessage string) error {
	msg := context
	if driverMessage != "" {
		msg += ": " + driverMessage
	}
	return &alpaca.AlpacaError{Number: alpaca.ErrNumDriverBase, Message: msg}
}

func sendErr(err error) error {
	if err == nil {
		return nil
	}
	if ns, ok := err.(notServing); ok {
		return NotConnected(ns.NotServingReason())
	}
	return DriverError("write to driver failed", err.Error())
}
