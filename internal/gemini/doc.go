// Package gemini talks to Gemini through Vertex AI from the server only.
//
// The credential is a service account key file resolved through settings.
// The project and the location arrive as ordinary settings. The model id
// arrives per call from settings, never from code. Every paid call reserves
// budget first, and the caller settles or releases it.
package gemini
