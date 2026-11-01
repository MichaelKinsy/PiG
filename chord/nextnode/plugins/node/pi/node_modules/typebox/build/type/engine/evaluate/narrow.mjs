// deno-fmt-ignore-file
import { Guard } from '../../../guard/index.mjs';
import { IsAny } from '../../types/any.mjs';
import { Never, IsNever } from '../../types/never.mjs';
import { IsUnknown } from '../../types/unknown.mjs';
import { Compare, CompareResultLeftInside, CompareResultRightInside, CompareResultEqual } from './compare.mjs';
import { Composite, CanComposite } from './composite.mjs';
function NarrowCompareRule(left, right) {
    const result = Compare(left, right);
    return (Guard.IsEqual(result, CompareResultLeftInside) ? left :
        Guard.IsEqual(result, CompareResultRightInside) ? right :
            Guard.IsEqual(result, CompareResultEqual) ? right :
                Never());
}
function NarrowCompositeRule(left, right) {
    const canCompositeLeft = CanComposite(left);
    const canCompositeRight = CanComposite(right);
    return (canCompositeLeft && canCompositeRight ? Composite(left, right) :
        canCompositeLeft && !canCompositeRight ? left :
            !canCompositeLeft && canCompositeRight ? right :
                NarrowCompareRule(left, right));
}
export function Narrow(left, right) {
    return (IsNever(left) ? left :
        IsAny(left) ? left :
            IsUnknown(left) ? right :
                IsNever(right) ? right :
                    IsAny(right) ? right :
                        IsUnknown(right) ? left :
                            NarrowCompositeRule(left, right));
}
