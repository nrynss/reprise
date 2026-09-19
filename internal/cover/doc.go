// Package cover builds one square image per episode from its title and
// show notes. The model draws an abstract panel with no faces and no
// text. The app sets the title, so the image never renders lettering. A
// model failure still leaves a deterministic fallback drawn from the
// episode number, so every episode ships a cover with no second paid
// call.
package cover
