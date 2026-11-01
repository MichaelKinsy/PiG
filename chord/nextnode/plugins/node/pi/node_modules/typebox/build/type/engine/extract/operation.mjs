// deno-lint-ignore-file ban-types
// deno-fmt-ignore-file
import { Guard } from '../../../guard/index.mjs';
import { IsUnion } from '../../types/union.mjs';
import { Extends, ExtendsResult } from '../../extends/index.mjs';
import { EvaluateType } from '../evaluate/evaluate.mjs';
import { EvaluateUnion } from '../evaluate/evaluate.mjs';
function ExtractType(left, right) {
    const check = Extends({}, left, right);
    const result = ExtendsResult.IsExtendsTrueLike(check) ? [left] : [];
    return result;
}
function ExtractUnion(left, right, result = []) {
    return Guard.ShiftLeft(left, (head, tail) => ExtractUnion(tail, right, [...result, ...ExtractType(head, right)]), () => result);
}
export function ExtractOperation(left, right) {
    const evaluated = EvaluateType(left);
    const canonical = IsUnion(evaluated) ? evaluated.anyOf : [evaluated];
    const remaining = ExtractUnion(canonical, right);
    const result = EvaluateUnion(remaining);
    return result;
}
