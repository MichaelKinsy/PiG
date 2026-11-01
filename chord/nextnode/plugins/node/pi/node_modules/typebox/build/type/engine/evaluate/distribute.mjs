// deno-fmt-ignore-file
// deno-fmt-ignore-file
import { Guard } from '../../../guard/index.mjs';
import { IsUnion } from '../../types/union.mjs';
import { Narrow } from './narrow.mjs';
import { EvaluateIntersect } from './evaluate.mjs';
import { EvaluateType } from './evaluate.mjs';
function ShouldEvaluate(left, right) {
    const result = IsUnion(left) || IsUnion(right);
    return result;
}
function DistributeOperation(left, right) {
    const evaluatedLeft = EvaluateType(left);
    const evaluatedRight = EvaluateType(right);
    const shouldEvaluate = ShouldEvaluate(evaluatedLeft, evaluatedRight);
    const result = shouldEvaluate
        ? EvaluateIntersect([evaluatedLeft, evaluatedRight])
        : Narrow(evaluatedLeft, evaluatedRight);
    return result;
}
function DistributeType(type, types, result = []) {
    return Guard.ShiftLeft(types, (left, right) => DistributeType(type, right, [...result, DistributeOperation(left, type)]), () => Guard.IsEqual(result.length, 0)
        ? [type]
        : result);
}
function DistributeUnion(types, distribution, result = []) {
    return Guard.ShiftLeft(types, (left, right) => DistributeUnion(right, distribution, [...result, ...Distribute([left], distribution)]), () => result);
}
export function Distribute(types, result = []) {
    return Guard.ShiftLeft(types, (left, right) => IsUnion(left)
        ? Distribute(right, DistributeUnion(left.anyOf, result))
        : Distribute(right, DistributeType(left, result)), () => result);
}
