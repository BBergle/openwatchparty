/**
 * A refusal or failure, from the relay or from the SDK itself.
 *
 * `message` is for logs only. For a relay error it is the relay's own text (SDK.md §15). Show users your own text for
 * `code`, and a generic text for codes you don't know.
 */
export class PartyError extends Error {
  /**
   * A relay error code from SPEC.md Appendix A, or one of the SDK's own: `invalid_code`, `unreachable` or `cancelled`.
   *
   * The relay's error codes are an open set.
   */
  readonly code: string;
  /** The type of the refused message when the relay named one, else null. */
  readonly re: string | null;

  constructor(code: string, message?: string, re: string | null = null) {
    super(message ?? code);
    this.name = "PartyError";
    this.code = code;
    this.re = re;
  }
}
