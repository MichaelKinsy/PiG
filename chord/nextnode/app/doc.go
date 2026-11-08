// Package app is Chord's app kit on Node's side (Pi's `src/node/app`, `@earendil-works/chord/app`): the storage an app's
// hosts share. This port carries the files of the kit (Files v2: spaces, versions, blobs, chunked uploads, live folder
// listings, copying a host's files) with the secrets and the data directory's layout they use; the rest of the kit
// (startServer, sessions, pairing, the owner link) follows in this package.
package app
