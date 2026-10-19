/// A copy of `relays.json` at the repository root, the public relay list the SDK starts from (SDK.md §3.1).
/// `RelayListTests` fails when it differs from that file.
let publicRelaysJSON = """
{
  "relays": [
    {
      "id": "na",
      "name": "North America",
      "url": "wss://na.relay.openwatch.party/v1/ws",
      "prefixes": "2345678"
    },
    {
      "id": "eu",
      "name": "Europe",
      "url": "wss://eu.relay.openwatch.party/v1/ws",
      "prefixes": "9ABCDEF"
    },
    {
      "id": "asia",
      "name": "Asia",
      "url": "wss://asia.relay.openwatch.party/v1/ws",
      "prefixes": "GHJKMNP"
    }
  ]
}

"""
