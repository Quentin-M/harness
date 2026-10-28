# Input: {k, samples: [{n, text}]} where text is a reviewer's raw output.
# Output: merged report. No LLM, no IO.

def sev_rank: {"blocker": 3, "major": 2, "minor": 1}[.] // 0;
def stop: ["the","and","for","not","with","that","this","when","from","into","can","are","was","but","has","have","does","its","via","than","then","will","may","only","also"];

def tokens:
  ascii_downcase | gsub("[^a-z0-9]+"; " ") | split(" ")
  | map(select(length >= 3 and (. as $t | stop | index($t) | not)) | sub("s$"; ""))
  | unique;

def first_json($re):
  try ([capture($re).j | fromjson] | first) catch null;

def extract_json:
  (try fromjson catch null) as $whole
  | if ($whole | type) == "object" then $whole
    else
      first_json("(?s)```(?:json)?\\s*(?<j>\\{.*\\})\\s*```") as $fence
      | if ($fence | type) == "object" then $fence
        else first_json("(?s)(?<j>\\{.*\\})") end
    end;

def num: (tonumber? // null);

def normalize_finding($n; $i):
  (.file // "" | tostring | sub("^\\./"; "")) as $file
  | (.line_start | num) as $a
  | ((.line_end | num) // $a) as $b
  | select(
      (.severity | IN("blocker", "major", "minor"))
      and ($file | length > 0)
      and ($a != null)
      and ((.title // "") | tostring | length > 0)
    )
  | {
      sample: $n, idx: $i, severity, file: $file,
      line_start: ([$a, $b] | min), line_end: ([$a, $b] | max),
      rule: (.rule // "" | tostring), title: (.title | tostring),
      claim: (.claim // "" | tostring), evidence: (.evidence // "" | tostring),
      fix: (.fix // "" | tostring),
      tok: ((.title | tostring) | tokens)
    };

def overlap_coef($a; $b):
  (($a | length) as $x | ($b | length) as $y
   | if ($x == 0 or $y == 0) then 0
     else (([$a[] | select(. as $t | $b | index($t))] | length) / ([$x, $y] | min)) end);

def iou($a; $b):
  (([$a.line_end, $b.line_end] | min) - ([$a.line_start, $b.line_start] | max) + 1) as $inter
  | if $inter <= 0 then 0
    else $inter / ((([$a.line_end, $b.line_end] | max) - ([$a.line_start, $b.line_start] | min)) + 1) end;

def near($a; $b):
  $a.file == $b.file
  and $a.line_start <= $b.line_end + 3 and $b.line_start <= $a.line_end + 3
  and (overlap_coef($a.tok; $b.tok) >= 0.25 or iou($a; $b) >= 0.5);

def cluster($items):
  reduce $items[] as $f ([];
    ([range(0; length) as $i | select((.[$i].samples | index($f.sample) | not) and any(.[$i].members[]; near(.; $f))) | $i] | first) as $at
    | if $at == null then . + [{members: [$f], samples: [$f.sample]}]
      else
        .[$at].members += [$f] | .[$at].samples += [$f.sample]
      end);

.k as $k
| (($k + 1) / 2 | floor) as $threshold
| ([.samples[] | . as $s | ($s.text | extract_json) as $o
    | if ($o | type) == "object" and ($o.findings | type) == "array"
      then {n: $s.n, ok: true, raw: ($o.findings | length),
            findings: [$o.findings | to_entries[] | .key as $i | .value | select(type == "object") | normalize_finding($s.n; $i)]}
      else {n: $s.n, ok: false, reason: (if ($s.text | length) == 0 then "empty output" else "no JSON object with a findings array" end)} end]) as $res
| ($res | map(select(.ok))) as $good
| ($res | map(select(.ok | not) | {sample: .n, reason})) as $failed
| ($good | map(.raw - (.findings | length)) | add // 0) as $dropped
| ([$good[].findings[]] | sort_by([-(.severity | sev_rank), .sample, .idx])) as $items
| (cluster($items)
   | map(
       . as $c
       | ($c.members | sort_by(-(.severity | sev_rank), -(.evidence | length))) as $m
       | {
           file: $m[0].file,
           line_start: ($c.members | map(.line_start) | min),
           line_end: ($c.members | map(.line_end) | max),
           title: $m[0].title,
           severity: $m[0].severity,
           votes: ($c.samples | length),
           samples: ($c.samples | sort),
           rule: ($m | map(.rule) | map(select(length > 0)) | first // ""),
           evidence: ($c.members | sort_by(-(.evidence | length)) | .[0].evidence),
           fix: ($m | map(.fix) | map(select(length > 0)) | first // ""),
           claim: $m[0].claim
         }
       | .confirmed = (.votes >= $threshold and (.severity | IN("blocker", "major")))
       | .blocker_singleton = (.votes < $threshold and .severity == "blocker")
     )
   | sort_by([-(.votes), -(.severity | sev_rank), .file, .line_start])
   | to_entries | map(.value + {id: (.key + 1)})) as $clusters
| ($good | length) as $ok
| {
    k: $k, ok: $ok, failed: $failed, dropped_findings: $dropped,
    threshold: $threshold,
    min_ok: ([2, $k] | min),
    clusters: $clusters,
    confirmed: ($clusters | map(select(.confirmed)) | length),
    blocker_singletons: ($clusters | map(select(.blocker_singleton)) | length),
    maybe: ($clusters | map(select(.confirmed | not)) | length)
  }
| .valid = ($ok >= .min_ok)
| .happy = (.valid and .confirmed == 0 and .blocker_singletons == 0)
