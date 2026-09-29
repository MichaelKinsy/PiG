// Independent SDK Sessions belong to the connection even while creation waits. Keeping ownership separate lets an unused SDK stay unloaded during shutdown.
const owners = new WeakMap();
export function independentSessionOwner(runtime) {
  let state = owners.get(runtime);
  if (!state) {
    state = { closed: false, pending: new Set(), sessions: new Set() };
    owners.set(runtime, state);
  }
  return state;
}

export async function disposeIndependentSessions(runtime) {
  const owner = independentSessionOwner(runtime);
  owner.closed = true;
  await Promise.allSettled([...owner.pending]);
  const results = await Promise.allSettled([...owner.sessions].map(async session => {
    try { await session.abort(); } finally { session.dispose(); }
  }));
  const failures = results.filter(result => result.status === "rejected").map(result => result.reason);
  if (failures.length) throw new AggregateError(failures, "Independent Session shutdown failed");
}
