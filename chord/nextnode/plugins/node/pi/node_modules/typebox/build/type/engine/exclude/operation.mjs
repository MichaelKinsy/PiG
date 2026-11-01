// deno-lint-ignore-file ban-types
// deno-fmt-ignore-file
import { Guard } from '../../../guard/index.mjs';
import { IsUnion } from '../../types/union.mjs';
import { Extends, ExtendsResult } from '../../extends/index.mjs';
import { EvaluateType } from '../evaluate/evaluate.mjs';
import { EvaluateUnion } from '../evaluate/evaluate.mjs';
function ExcludeType(left, right) {
    const check = Extends({}, left, right);
    const result = ExtendsResult.IsExtendsTrueLike(check) ? [] : [left];
    return result;
}
function ExcludeUnion(left, right, result = []) {
    return Guard.ShiftLeft(left, (head, tail) => ExcludeUnion(tail, right, [...result, ...ExcludeType(head, right)]), () => result);
}
export function ExcludeOperation(left, right) {
    const evaluated = EvaluateType(left);
    const canonical = IsUnion(evaluated) ? evaluated.anyOf : [evaluated];
    const remaining = ExcludeUnion(canonical, right);
    const result = EvaluateUnion(remaining);
    return result;
}
