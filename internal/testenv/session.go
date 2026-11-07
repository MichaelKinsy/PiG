package testenv

// SessionHelper is the environment variable that makes a test binary whose TestMain calls ReportSessionIfRequested print the
// ID of its session and exit.
const SessionHelper = "PIG_TEST_SESSION_HELPER"
