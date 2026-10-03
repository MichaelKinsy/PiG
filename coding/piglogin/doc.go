// Package piglogin is PiG's built-in sprite login: the sprite's 16-by-14 pig, which the startup header shows in Pi's logo
// slot, the full login art (the 12-pixel "PiG." hero wordmark with its drop shadow beside that pig) that /sprite preview shows,
// the sprites `/sprite` chooses from, and the selection kept under $PIG_HOME/state/pig-standard/login.json.
//
// The artwork, sprite names and colors, the login definition and the golden renders of the color sprites and the sheriff
// are PiG Standard's piglogin extension (piglets/standard/extensions/piglogin at MichaelKinsy/PiG d86eb93), written by
// Michael Kinsy and carried by Pigpen's pig-login Package. Its LICENSE is MIT, Copyright Hewlett Packard Enterprise
// Development LP. The character sprites (PiGrogu, Darth Vader, Kratos, Piglet, Spider-Ham) are the owner's earlier PiG
// piglogin. The pixel data is unchanged.
//
// pig divergence (D2): Pi's startup header draws Pi's logo (pi-logo.ts, interactive-mode.ts:998-1006); PiG draws the
// sprite's pig.
package piglogin
