// deno-fmt-ignore-file
import { Guard } from '../../../guard/index.mjs';
import { IsUnion } from '../../types/union.mjs';
function FlattenType(type) {
    const result = IsUnion(type) ? Flatten(type.anyOf) : [type];
    return result;
}
export function Flatten(types, result = []) {
    return Guard.ShiftLeft(types, (left, right) => Flatten(right, [...result, ...FlattenType(left)]), () => result);
}
