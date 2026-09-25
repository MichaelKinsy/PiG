// Use pinned Chord state directly: the connection oracle's local substitute cannot prove subscription semantics.
import { MutableReplicatedStateImpl } from '../../../../.upstream/current/packages/chord/src/services/state.ts';
import { BACKGROUND_CONTEXT } from '../../../../.upstream/current/packages/chord/src/context/index.ts';

const state = new MutableReplicatedStateImpl({n: 0});
const events = [];
let removeSecond;
state.subscribe((value, context, delivery) => {
  if (delivery.kind !== 'update') return;
  events.push(`first:${value.n}`);
  if (value.n === 1) {
    state.replace(context, {n: 2});
    removeSecond();
  }
});
removeSecond = state.subscribe((value, _context, delivery) => {
  if (delivery.kind === 'update') events.push(`second:${value.n}`);
});
state.replace(BACKGROUND_CONTEXT, {n: 1});
console.log(JSON.stringify(events));
