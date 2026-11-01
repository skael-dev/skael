type ApiResult = { error?: unknown; response?: Response };

type ErrorBody = { detail?: string; errors?: { message?: string }[] };

const DEFAULT_STEPS: Record<number, string> = {
  403: "Ask an owner or an admin to make this change.",
  404: "Reload the page and try again.",
  409: "Choose another name.",
  422: "Correct the value and try again.",
};

// The message says what failed and what to do next. `steps` replaces the
// default next step for a status.
export function apiErrorMessage(
  action: string,
  result: ApiResult,
  steps: Record<number, string> = {},
): string {
  const status = result.response?.status;
  if (status === undefined) {
    return `Could not ${action}. Check that the server is reachable and try again.`;
  }
  if (status >= 500) {
    return `Could not ${action}. The server failed: check its logs and try again.`;
  }
  const body = (result.error ?? {}) as ErrorBody;
  const reason = body.errors?.[0]?.message ?? body.detail;
  const step = steps[status] ?? DEFAULT_STEPS[status] ?? "Try again.";
  return reason ? `Could not ${action}: ${reason}. ${step}` : `Could not ${action}. ${step}`;
}

export function throwOnApiError(
  action: string,
  result: ApiResult,
  steps?: Record<number, string>,
): void {
  if (result.error !== undefined) throw new Error(apiErrorMessage(action, result, steps));
}
