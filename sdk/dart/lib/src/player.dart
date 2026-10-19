/// The app's player, read and commanded by the SDK (SDK.md §16.4).
///
/// Extend it (`final class MyAdapter extends PlayerAdapter`): a member added in a later 1.x release comes with a
/// default, so subclasses keep compiling. Reads are cheap and synchronous; the SDK reads them on every tick. Commands
/// complete when the player has done them, or throw; until one does, the SDK sends this adapter nothing else. The
/// adapter doesn't report the user's presses: the app sends them through the party.
abstract base class PlayerAdapter {
  /// Current position in the app's own file.
  int get positionMs;

  /// The file's duration; null when unknown. Without it the SDK doesn't protect the end of a shorter file.
  int? get durationMs;

  bool get paused;

  /// Stalled while trying to play.
  bool get buffering;

  /// The file is open and shows a frame. May stay false until the app's own start-up work is done.
  bool get firstFrameShown;

  /// Whether [setRate] works now. May change, for instance to false while audio passthrough is on.
  bool get canSetRate => false;

  /// How far from the requested position a [seek] may land, in ms: a player that seeks to keyframes declares its
  /// keyframe distance, and one that can't seek at all a very large value, so the SDK follows by rate only. Every seek
  /// threshold of the SDK is at least this. Below 0 counts as 0.
  int get seekAccuracyMs => 0;

  /// Throws when the player refuses to start, such as a browser blocking playback without a user gesture. When the
  /// player then reads [paused], the SDK emits `PlayBlockedEvent` and reports `loading` until it plays (SDK.md §10.2).
  Future<void> play();

  Future<void> pause();

  /// Completes once the seek has landed. Completing early makes the SDK measure mid-seek positions.
  Future<void> seek(int positionMs);

  Future<void> setRate(double rate) async {}
}
