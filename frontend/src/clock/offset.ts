// Every browser has its own clock, and those clocks are rarely perfectly
// accurate — a laptop might be a few seconds fast or slow compared to the
// server. Since every window's playback timing is computed from "what time
// is it right now", a wrong local clock would make a browser show the
// wrong item, or show the right item at the wrong point in its progress.
//
// This file fixes that by measuring the difference ("offset") between the
// browser's clock and the server's clock, so the rest of the app can ask
// "what does the SERVER think the time is right now?" instead of trusting
// the browser's own clock directly.
import { getTime } from "../api/client";

interface Sample {
  offsetMs: number;
  roundTripMs: number;
}

// takeSample sends one request to GET /api/time and estimates the clock
// offset from it.
//
// Worked example: we send the request when our local clock reads 1000ms.
// The response arrives when our local clock reads 1040ms — a 40ms round
// trip. We assume the request took the same time to arrive as the response
// took to come back (half of 40ms = 20ms each way), so the server's clock
// must have read "1000 + 20 = 1020ms" (our time) at the moment it recorded
// its own answer. If the server's answer says its clock was actually at
// 1050ms at that moment, our clock is running 30ms behind the server's
// (offset = 1050 - 1020 = +30). Adding that offset to our local clock from
// then on gives us the server's clock.
async function takeSample(baseUrl: string): Promise<Sample> {
  const sentAt = Date.now();
  const { server_time } = await getTime(baseUrl);
  const receivedAt = Date.now();

  const roundTripMs = receivedAt - sentAt;
  const serverTimeMs = Date.parse(server_time);
  const estimatedServerTimeAtRequest = sentAt + roundTripMs / 2;
  const offsetMs = serverTimeMs - estimatedServerTimeAtRequest;

  return { offsetMs, roundTripMs };
}

// ClockSync keeps track of the current best estimate of the offset between
// this browser's clock and the server's clock, and refreshes it
// periodically (network conditions change, so one measurement isn't
// trusted forever).
export class ClockSync {
  private offsetMs = 0;

  constructor(private readonly baseUrl: string) {}

  // serverNow() is what every scheduling calculation in this app should
  // use instead of Date.now() directly — it's our best guess at what the
  // server's clock reads at this exact moment.
  serverNow(): number {
    return Date.now() + this.offsetMs;
  }

  get currentOffsetMs(): number {
    return this.offsetMs;
  }

  // refresh takes several samples (a few round trips to the server) and
  // keeps only the one with the shortest round trip time, since a fast
  // round trip means less uncertainty about how long the request and
  // response each took — and therefore a more trustworthy offset estimate.
  // A single slow, congested sample would otherwise be free to throw the
  // estimate off by however long that congestion added.
  async refresh(sampleCount = 5): Promise<void> {
    let best: Sample | null = null;

    for (let i = 0; i < sampleCount; i++) {
      try {
        const sample = await takeSample(this.baseUrl);
        if (!best || sample.roundTripMs < best.roundTripMs) {
          best = sample;
        }
      } catch {
        // A single failed sample (e.g. a dropped request) just means one
        // fewer data point — the loop keeps trying the rest.
      }
    }

    if (best) {
      this.offsetMs = best.offsetMs;
    }
  }
}
