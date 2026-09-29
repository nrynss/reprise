// Package mail sends mail through Resend from the server only. It is the
// only code that talks to Resend. Consumers declare the Sender interface
// where they use it. Tests use Fake or a local HTTP server.
package mail
