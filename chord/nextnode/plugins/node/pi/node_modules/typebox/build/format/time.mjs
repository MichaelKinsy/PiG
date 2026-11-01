const TIME = /^(\d\d):(\d\d):(\d\d)(?:\.\d+)?(?:([Zz])|([+-])(\d\d):(\d\d))?$/;
/**
 * Returns true if the value is an ISO time string
 * @specification https://datatracker.ietf.org/doc/html/rfc3339
 */
export function IsTime(value, strictTimeZone = true) {
    const matches = TIME.exec(value);
    if (!matches)
        return false;
    // Require timezone offset or 'Z'/'z' when strictTimeZone is true
    if (strictTimeZone && !matches[4] && !matches[5])
        return false;
    const hr = +matches[1];
    const min = +matches[2];
    const sec = +matches[3];
    if (hr > 23 || min > 59 || sec > 60)
        return false;
    if (matches[5]) {
        const tzH = +matches[6];
        const tzM = +matches[7];
        if (tzH > 23 || tzM > 59)
            return false;
    }
    if (sec < 60)
        return true;
    // Leap second handling: must normalize to 23:59:60 UTC (1439 total UTC minutes)
    const tzSign = matches[5] === '-' ? -1 : 1;
    const tzH = +(matches[6] || 0);
    const tzM = +(matches[7] || 0);
    const totalUtcMin = (hr * 60 + min) - tzSign * (tzH * 60 + tzM);
    return (totalUtcMin % 1440 + 1440) % 1440 === 1439;
}
