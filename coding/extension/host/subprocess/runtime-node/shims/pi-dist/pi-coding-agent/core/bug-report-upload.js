import { bugReportFiles } from "./bug-report.js";
import { getRadiusGatewayUrl } from "./radius.js";
/** Upload a report as multipart form data, anonymously or attributed to a Radius account. */
export async function uploadBugReport(bundle, options = {}) {
    const body = new FormData();
    for (const file of bugReportFiles(bundle)) {
        body.append(file.name, new Blob([file.data], { type: file.contentType }), file.name);
    }
    const response = await fetch(new URL("/v1/bug-reports", options.gatewayUrl ?? getRadiusGatewayUrl()), {
        method: "POST",
        headers: options.token ? { Authorization: `Bearer ${options.token}` } : undefined,
        body,
        signal: options.signal,
    });
    const json = (await response.json().catch(() => null));
    if (response.ok && json?.ok === true && typeof json.bug_report?.id === "string") {
        return { id: json.bug_report.id };
    }
    const detail = json && !json.ok ? json.description || json.error : undefined;
    throw new Error(`Bug report upload failed: ${detail || response.statusText || response.status}`);
}
//# sourceMappingURL=bug-report-upload.js.map