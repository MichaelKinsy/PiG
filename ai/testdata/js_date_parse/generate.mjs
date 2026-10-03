// Regenerates corpus.json: `node generate.mjs > corpus.json`. Each row is a string and the value Node's Date.parse returns for it
// (null for NaN) with the process time zone set to UTC, America/New_York and Asia/Kolkata. V8's legacy parser reads a string that
// names no zone in local time, so the three zones tell a zone-free form from a zoned one. The first section is hand-written (ES5
// ISO forms, HTTP-dates, RFC 850, asctime, legacy month-name forms, rejects and range edges); the rest is a seeded random
// composition of the same tokens, ASCII only. The Go test reads Latin-1 bytes where Node reads UTF-16.
const inputs = [
 // ES5 ISO
 "2026-01-01","2026-01","2026","2026-01-01T00:00:00Z","2026-01-01T00:00:00.123Z","2026-01-01T00:00:00.1Z","2026-01-01T00:00:00.123456789Z","2026-01-01T00:00Z","2026-01-01T12:34:56+05:30","2026-01-01T12:34:56-0800","2026-01-01T12:34:56","2026-01-01T12:34","2026-01-01T24:00:00Z","2026-01-01T24:00:01Z","2026-01-01T24:01Z","2026-01-01T25:00Z","2026-13-01","2026-00-01","2026-01-32","2026-02-30T00:00:00Z","+002026-01-01T00:00:00Z","-000001-01-01T00:00:00Z","-000000-01-01T00:00:00Z","+275760-09-13T00:00:00.000Z","+275760-09-13T00:00:00.001Z","-271821-04-20T00:00:00Z","2026-01-01T00:00:00+24:00","2026-01-01T00:00:00+23:59","2026-01-01T00:00:00+0560",
 "2026-01-01t00:00:00z","2026-01-01 00:00:00","2026-01-01 00:00:00Z","2026-01-01 12:00 +0100","2026-1-1","2026/01/01","2026/1/2 3:04:05","2026-01-01T",
 "2026-01-01T00:00:00Zjunk","2026-01-01T00","2026-01-01T1:00:00Z",
 // HTTP date, RFC 850, asctime
 "Thu, 01 Jan 2026 00:00:00 GMT","Thu, 01 Jan 2026 00:00:00 UTC","Thursday, 01-Jan-26 00:00:00 GMT","Thu Jan  1 00:00:00 2026","Thu Jan 01 2026 00:00:00 GMT+0000","Thu Jan 01 2026 00:00:00 GMT+0100 (CET)","Thu, 01 Jan 2026 00:00:00 +0200","Thu, 01 Jan 2026 00:00:00 -0500","Thu, 01 Jan 2026 00:00:00 EST","Thu, 01 Jul 2026 00:00:00 PDT","Thu, 01 Jan 2026 00:00:00 CST","Thu, 01 Jan 2026 00:00:00 MST","Thu, 01 Jan 2026 00:00:00 XYZ","Sun, 06 Nov 1994 08:49:37 GMT","Sunday, 06-Nov-94 08:49:37 GMT","Sun Nov  6 08:49:37 1994","Mon, 31 Dec 2029 23:59:59 GMT","Fri, 32 Jan 2026 00:00:00 GMT","Fri, 31 Feb 2026 00:00:00 GMT",
 "01 Jan 2026","1 January 2026","January 1, 2026","Jan 1, 2026 10:00","January 1, 2026 10:00 PM","January 1, 2026 12:00 AM","January 1, 2026 12:30 PM","January 1, 2026 13:00 PM","January 1, 2026 0:30 AM","Jan 5","Jan 5 10:00","5 Jan","May 5, 99","May 5, 49","May 5, 50","May 5, 0","Sept 5, 2026","Sep 5, 2026","Septembre 5 2026","1/2/2026","12/31/2026","13/1/2026","1/2/26","1/2","2026 Jan 1","2026 1 Jan","Jan 2026","1 2 3","10 20 30","99 1 1","2026-01-01 Jan","12:30","12:30:45","12:30:45.5","12:30:45.123456","12:30:45 PM","1:2:3 4","24:00","24:00:00","24:00:01","Jan 1 2026 24:00","Jan 1 2026 25:00",
 "Jan 1 2026 10:00:00 GMT-8","Jan 1 2026 10:00:00 GMT-08:00","Jan 1 2026 10:00:00 UTC+1","Jan 1 2026 10:00:00 Z","Jan 1 2026 10:00:00 +01","Jan 1 2026 10:00:00 +1","Jan 1 2026 10:00:00 +12345","Jan 1 2026 10:00:00 +1230","Jan 1 2026 10:00:00 +123","Jan 1 2026 10:00:00 -0","Jan 1 2026 10:00:00 UT","Jan 1 2026 10:00 EDT","Jan 1 2026 (comment) 10:00","Jan 1 2026 (nested (comment)) 10:00","Jan 1 2026 (unclosed","Jan 1 2026 )","Jan 1 2026 ,","Jan 1, 2026,","Jan-1-2026","1-Jan-2026","1-Jan-26","Jan.1.2026","Jan 1 2026 10:00:00.5","Jan 1 2026 10:00:00.","Jan 1 2026 10.5",
 // garbage / non dates
 "","  ","not-a-date","not-a-date-or-number","120 seconds","tomorrow","Infinity","NaN","0","1","12","123","1234","12345","123456","1234567890","99999999999","0000","00","-1","+1","1e3","1.5","  2026-01-01  ","\t2026-01-01T00:00:00Z","2026-01-01T00:00:00Z\n","Thu, 01 Jan 2026 00:00:00 GMT junk","junk Thu, 01 Jan 2026 00:00:00 GMT","junk 2026-01-01","2026-01-01 junk","Tuesday 2026-01-01","Foo 1 2026","Jan 1 2026 foo","Janx 1 2026","Mayo 5 2026","Decembre 25 2026","AM","PM 10","Z","T","10 AM","10:00 AM","10:00 am","10:00AM","10:00 A.M.","a b c 10:00",
 "275760-09-13","Sat, 13 Sep 275760 00:00:00 GMT","Sat, 13 Sep 275760 00:00:01 GMT","Jan 1 -271821","Jan 1 100000000","Jan 1 999999999","Jan 1 1000000000","Jan 1 12345678901234567890","Jan 1 0","Jan 1 00","Jan 1 000","Jan 1 0000","Jan 1 1",
 "1970-01-01T00:00:00Z","1969-12-31T23:59:59.999Z","0001-01-01T00:00:00Z","0000-01-01T00:00:00Z","9999-12-31T23:59:59.999Z","10000-01-01","99999-01-01",
 "2026-03-08T02:30:00","2026-11-01T01:30:00","2026-07-01T12:00:00","Mar 8 2026 2:30","Nov 1 2026 1:30","Jul 1 2026 12:00",
 "2026-01-01T00:00:00.000+00:00","2026-01-01T00:00:00,123Z","2026-01-01T00:00:00 Z","2026-01-01T00:00:00 +01:00","2026-W01-1","2026-001","20260101","20260101T000000Z",
 "Thu, 01 Jan 2026 00:00:00","Thu, 01 Jan 2026","Thu 01 Jan 2026 00:00:00 GMT+1","Thu, 01 Jan 2026 00:00:00 GMT+01:00","Thu, 01 Jan 2026 00:00:00 GMT-0100","Thu, 01 Jan 2026 00:00:00 GMT -0100","Thu, 01 Jan 2026 00:00:00 GMT + 0100","Thu, 01 Jan 2026 00:00:00 +0100 (CET)",
 "Wed Jan 01 2026","Wednesday January 1 2026","Mon, 1 Jan 2026 00:00:00 GMT","tue, 1 jan 2026 0:0:0 gmt","THU, 01 JAN 2026 00:00:00 GMT",
 "1 Jan 2026 00:00:00 GMT","1 Jan 2026 0:00","1 Jan 2026 00:00:00:00","1 Jan 2026 00:00:00::","1 Jan 2026 10::","1 Jan 2026 10:","1 Jan 2026 10:00:","Jan 1 2026 ::",
 "Jan 32 2026","Jan 31 2026","Feb 30 2026","Feb 29 2026","Feb 29 2028","Dec 31 2026 23:59:59.999","Dec 31 2026 23:59:60","Dec 31 2026 23:60","Dec 31 2026 24:00:00.000","Dec 31 2026 24:00:00.001",
 "2026-1-01","2026-01-1","2026-01-01T0:00Z","2026-01-01T00:0Z","2026-01-01T00:00:0Z","2026-01-01T00:00:00.Z","2026-01-01T00:00:00.1234567890123Z","2026-01-01T00:00:00+1Z","2026-01-01T00:00:00+01","2026-01-01T00:00:00+01:0","2026-01-01T00:00:00+0100:","-2026-01-01","+2026-01-01","+02026-01-01","+0020260-01-01",
 // UTC offset hours past 2^30 seconds: V8 rejects a total above Smi::kMaxValue, which is 2^31-1 in Node (no pointer compression)
 "Oct 2 2025 GMT+298262:0","Oct 2 2025 GMT+520251:2","Oct 2 2025 GMT-596523:0","Oct 2 2025 GMT+596524:0","Oct 2 2025 GMT+999999999:0","+275760:GMT+520251:2Oct(MDT",
];
const vocab = ["2026","26","1","01","12","31","00","24","60","999","99999","0","-","+",":",".","/"," "," ","  ",",","(c)","(","(x(y))",")","T","t","Z","z","GMT","UTC","EST","PDT","Jan","jan","May","March","Sept","Thu","Thursday","AM","PM","am","a.m.","foo","\t",":30",":45",".5",".123","10:30","10:30:45","+0100","-08:00","+05:30","1.","x","12345678901","000","20260101"];
let seed = 12345;
const rnd = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
const seen = new Set(inputs);
let valid = 0, invalid = 0;
for (let i = 0; i < 200000 && valid + invalid < 1200; i++) {
  const n = 1 + Math.floor(rnd() * 8);
  let s = "";
  for (let j = 0; j < n; j++) s += vocab[Math.floor(rnd() * vocab.length)];
  if (seen.has(s)) continue;
  const ok = !Number.isNaN(Date.parse(s));
  if (ok ? valid >= 700 : invalid >= 500) continue;
  seen.add(s);
  inputs.push(s);
  if (ok) valid++; else invalid++;
}
const zones = ["UTC", "America/New_York", "Asia/Kolkata"];
const rows = inputs.map((input) => ({ input, ms: [] }));
for (const zone of zones) {
  process.env.TZ = zone;
  rows.forEach((row) => {
    const value = Date.parse(row.input);
    row.ms.push(Number.isNaN(value) ? null : value);
  });
}
console.log(JSON.stringify(rows.map((row) => ({ input: row.input, utc: row.ms[0], newYork: row.ms[1], kolkata: row.ms[2] })), null, 0).replaceAll("},{", "},\n{"));
