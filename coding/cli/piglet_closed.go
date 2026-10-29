package cli

// currentPigletBinary is the executable's Piglet Binary state; tests replace it.
var currentPigletBinary = runningPigletBinary

func pigletBinaryClosed(binary pigletBinary) bool { return false }

func pigletBinaryExtensionFlagRefusal(binary pigletBinary, extensions []string) string { return "" }

func pigletBinarySettingsExtensionsRefusal(binary pigletBinary, global, project []string) string {
	return ""
}

func pigletBinaryPackageCommandRefusal(binary pigletBinary, opts *packageCLIOptions) string {
	return ""
}

func pigletBinaryMcpRefusal(binary pigletBinary, args []string) string { return "" }
