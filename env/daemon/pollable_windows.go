//go:build windows

package daemon

import "io"

// pollableReader and pollableWriter are the identity: Windows has no inherited blocking descriptor to move into the poller.
func pollableReader(reader io.Reader) (io.Reader, func()) { return reader, func() {} }

func pollableWriter(writer io.Writer) (io.Writer, func()) { return writer, func() {} }
