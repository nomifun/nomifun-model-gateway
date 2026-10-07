// SPDX-License-Identifier: Apache-2.0
export type MutationResult<T> = { ok: true; value: T; feedbackError?: unknown } | { ok: false; error: unknown };
// A completed write remains successful even if its subsequent UI feedback
// fails. One-time redemption/credentials must never invite a duplicate write.
export async function runMutation<T>(mutate: () => Promise<T>, onSuccess?: (value: T) => unknown | Promise<unknown>): Promise<MutationResult<T>> {
  let value: T;
  try { value = await mutate(); } catch (error) { return { ok: false, error }; }
  try { await onSuccess?.(value); return { ok: true, value }; } catch (feedbackError) { return { ok: true, value, feedbackError }; }
}
