// deno-fmt-ignore-file
import { Guard } from '../../../guard/index.mjs';
import { IsAny } from '../../types/any.mjs';
import { IsNever } from '../../types/never.mjs';
import { IsObject } from '../../types/object.mjs';
import { IsUnknown } from '../../types/unknown.mjs';
import { Compare, CompareResultLeftInside, CompareResultEqual, CompareResultDisjoint } from './compare.mjs';
import { Flatten } from './flatten.mjs';
import { EvaluateType } from './evaluate.mjs';
function BroadenFilter(type, types, result = [], all = types) {
    return Guard.ShiftLeft(types, (left, right) => {
        const compare = Compare(type, left);
        return ((Guard.IsEqual(compare, CompareResultLeftInside) || Guard.IsEqual(compare, CompareResultEqual))
            ? all // Left in set. Return original All set.
            : Guard.IsEqual(compare, CompareResultDisjoint)
                ? BroadenFilter(type, right, [...result, left], all) // Left is disjoint, keep it
                : BroadenFilter(type, right, result, all) // Left in Type, drop it
        );
    }, () => [...result, type]); // Type broadest in set
}
function BroadenType(type, types, result) {
    const evaluated = EvaluateType(type);
    return (IsAny(evaluated) ? [evaluated] : // terminate (always the most broad)
        IsUnknown(evaluated) ? [evaluated] : // terminate (always the most broad)
            IsNever(evaluated) ? BroadenTypes(types, result) : // ignored: never is dropped
                IsObject(evaluated) ? BroadenTypes(types, [...result, evaluated]) : // objects are always considered (too expensive to compare)
                    BroadenTypes(types, BroadenFilter(evaluated, result)));
}
function BroadenTypes(types, result = []) {
    return Guard.ShiftLeft(types, (left, right) => (BroadenType(left, right, result)), () => result);
}
/** Broadens a set of types and returns either the most broad type, or union or disjoint types. */
export function Broaden(types) {
    const broadened = BroadenTypes(types);
    const flattened = Flatten(broadened);
    return flattened;
}
