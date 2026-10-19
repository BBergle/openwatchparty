/// Why an operation failed or the relay refused a command.
///
/// [code] is one of the relay's error codes (an open set, so a code a newer relay sends passes through) or one of the
/// SDK's own: [ErrorCode.invalidCode], [ErrorCode.unreachable] and [ErrorCode.cancelled].
final class PartyException implements Exception {
  const PartyException(this.code, [this.message = '', this.re]);

  final String code;

  /// Text for logs only (SDK.md §15): the relay's `error.message` when the relay refused, else the SDK's English text.
  /// Don't parse it or show it to users; map [code] to your own text, with a generic one for codes you don't know.
  final String message;

  /// The type of the refused message, when a relay refused one.
  final String? re;

  @override
  String toString() => 'PartyException($code${re == null ? '' : ', re: $re'}${message.isEmpty ? '' : ': $message'})';
}

/// The error codes the SDK knows. Others pass through as strings.
abstract final class ErrorCode {
  static const invalidMessage = 'invalid_message';
  static const unsupported = 'unsupported';
  static const rateLimited = 'rate_limited';
  static const roomNotFound = 'room_not_found';
  static const roomFull = 'room_full';

  /// Every relay that answered refused `create` because it is full: the service is full; try later.
  static const relayFull = 'relay_full';
  static const roomLocked = 'room_locked';
  static const forbidden = 'forbidden';
  static const notInRoom = 'not_in_room';
  static const alreadyInRoom = 'already_in_room';
  static const resumeFailed = 'resume_failed';
  static const clientBlocked = 'client_blocked';

  /// A typed code that isn't 6 symbols of the code alphabet.
  static const invalidCode = 'invalid_code';

  /// No relay could be reached, or none answered in time.
  static const unreachable = 'unreachable';

  /// `leave()` or `close()` cancelled a `create` or `join` that was under way.
  static const cancelled = 'cancelled';
}
