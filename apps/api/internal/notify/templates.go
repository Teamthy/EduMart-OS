package notify

import "fmt"

// Template keys. They are stored on every delivery row, which is how an operator can tell "verification
// emails are failing" from "the relay is down for everything".
const (
	TemplateVerifyEmail   = "verify_email"
	TemplateVerifyPhone   = "verify_phone"
	TemplatePasswordReset = "password_reset"
	TemplateInvitation    = "invitation"
	TemplateReceipt       = "purchase_receipt"
	TemplateRefund        = "refund_receipt"
)

// template is one message shape: subject plus body, built from named values.
type template struct {
	subject string
	body    func(values map[string]string) string
}

var templates = map[string]template{
	TemplateVerifyEmail: {
		subject: "Your Edumart verification code",
		body: func(v map[string]string) string {
			return fmt.Sprintf(`Hi %s,

Your Edumart verification code is %s.

It expires in %s and can be used once. If you did not ask for it, you can ignore this message —
nobody can access your account without the code.

— Edumart`, v["name"], v["code"], v["ttl"])
		},
	},
	TemplateVerifyPhone: {
		subject: "Your Edumart verification code",
		body: func(v map[string]string) string {
			return fmt.Sprintf("Edumart: your verification code is %s. It expires in %s.", v["code"], v["ttl"])
		},
	},
	TemplateInvitation: {
		subject: "You have been invited to an Edumart institution",
		body: func(v map[string]string) string {
			return fmt.Sprintf(`Hi,

%s added you to %s on Edumart as %s.

Accept here: %s

The link works once and expires in %s. If you were not expecting this, ignore the message and nothing
will happen to your account.

— Edumart`, v["inviter"], v["organisation"], v["role"], v["link"], v["ttl"])
		},
	},
	TemplateReceipt: {
		subject: "Your Edumart receipt",
		body: func(v map[string]string) string {
			return fmt.Sprintf(`Hi %s,

Thank you — your payment went through.

Reference: %s
Items: %s
Total: %s
Paid: %s

What you bought is available in your library now. Keep this email — it is your receipt, and the
reference above is what support will ask for.

— Edumart`, v["name"], v["reference"], v["items"], v["total"], v["paid"])
		},
	},
	TemplateRefund: {
		subject: "Your Edumart refund",
		body: func(v map[string]string) string {
			return fmt.Sprintf(`Hi %s,

We have refunded %s on order %s.

Your bank or payment provider may take a few working days to show it. If you bought something with
that payment, access to it ends now — the refund returns the money and the product together.

Keep this email: the reference above is what support will ask for.

— Edumart`, v["name"], v["amount"], v["reference"])
		},
	},
	TemplatePasswordReset: {
		subject: "Reset your Edumart password",
		body: func(v map[string]string) string {
			return fmt.Sprintf(`Hi %s,

Someone asked to reset the password for your Edumart account. Use this code to choose a new one:

%s

It expires in %s and works once. Resetting your password signs you out of every device. If this was
not you, no action is needed — your password has not changed.

— Edumart`, v["name"], v["code"], v["ttl"])
		},
	},
}

// Render builds a Message for a template, or falls back to plain values when the key is unknown.
//
// Templates are code, not database rows, on purpose: a message that can be edited at runtime by anyone
// with database access is a phishing vector aimed at our own users.
func Render(key string, channel Channel, to string, values map[string]string) Message {
	tpl, ok := templates[key]
	if !ok {
		return Message{
			Channel:     channel,
			To:          to,
			Subject:     "Edumart",
			Text:        values["code"],
			TemplateKey: key,
			Code:        values["code"],
		}
	}
	return Message{
		Channel:     channel,
		To:          to,
		Subject:     tpl.subject,
		Text:        tpl.body(values),
		TemplateKey: key,
		Code:        values["code"],
	}
}
