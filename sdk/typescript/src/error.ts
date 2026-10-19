/**
 * A refusal or failure. `code` is a relay error code (SPEC.md Appendix A, an open set) or one of the SDK's own:
 * `invalid_code`, `unreachable`, `cancelled`. `re` is the type of the refused message when the relay named one, else
 * null. `message` is for logs only: for a relay error it is the relay's own text (SDK.md §15). Show users your own text
 * for `code`, and a generic one for codes you don't know.
 */
export class PartyError extends Error {
  readonly code: string;
  readonly re: string | null;

  constructor(code: string, message?: string, re: string | null = null) {
    super(message ?? code);
    this.name = "PartyError";
    this.code = code;
    this.re = re;
  }
}
