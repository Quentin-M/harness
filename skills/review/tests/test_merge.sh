#!/usr/bin/env bash
# Behavior tests for scripts/merge. Fixtures are written per case into a fresh temp dir.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
merge=$here/../scripts/merge
root=$(mktemp -d /tmp/review-merge-test.XXXXXX)
pass=0 fail=0

finding() {  # severity file start end title evidence
  jq -nc --arg s "$1" --arg f "$2" --argjson a "$3" --argjson b "$4" --arg t "$5" --arg e "${6:-quote of $5}" \
    '{severity: $s, file: $f, line_start: $a, line_end: $b, rule: "invariant", title: $t, claim: "claim", evidence: $e, fix: "fix"}'
}
sample() {  # dir n finding...
  local dir=$1 n=$2; shift 2
  mkdir -p "$dir"
  printf '{"summary":"s","findings":[%s]}\n' "$(IFS=,; echo "$*")" > "$dir/sample-$n.out"
}
run() { "$merge" "$1" --samples "$2" --json 2>"$1/stderr"; echo $? > "$1/exit"; }
check() {  # name jq-filter dir
  if jq -e "$2" "$3/merged.json" >/dev/null 2>&1; then pass=$((pass + 1)); echo "ok   $1"
  else fail=$((fail + 1)); echo "FAIL $1"; jq -c . "$3/merged.json"; fi
}
check_exit() {
  if [[ $(cat "$2/exit") == "$3" ]]; then pass=$((pass + 1)); echo "ok   $1"; else fail=$((fail + 1)); echo "FAIL $1: exit $(cat "$2/exit"), want $3"; fi
}

# 1. same bug in 2 of 3 samples with different wording and shifted lines -> one confirmed cluster, 2 votes
d=$root/vote; 
sample "$d" 1 "$(finding major src/a.py 10 12 'Off-by-one in loop bound skips last item' 'range(n - 1)')"
sample "$d" 2 "$(finding blocker src/a.py 11 11 'Loop bound off by one drops final element' x)"
sample "$d" 3
run "$d" 3
check "2/3 agreement clusters into one confirmed finding" '(.clusters|length)==1 and .clusters[0].votes==2 and .clusters[0].confirmed and .confirmed==1' "$d"
check "cluster severity is the max across members" '.clusters[0].severity=="blocker"' "$d"
check "cluster keeps the longest evidence quote" '.clusters[0].evidence=="range(n - 1)"' "$d"
check "cluster spans the union of line ranges" '.clusters[0].line_start==10 and .clusters[0].line_end==12' "$d"
check "confirmed finding makes the run not happy" '.happy==false' "$d"

# 2. singleton major is maybe, not confirmed; run stays happy
d=$root/singleton
sample "$d" 1 "$(finding major src/a.py 5 5 'Possible race on cache fill')"
sample "$d" 2
sample "$d" 3
run "$d" 3
check "singleton major is unconfirmed" '.confirmed==0 and .maybe==1 and .clusters[0].confirmed==false' "$d"
check "singleton major does not block" '.happy==true' "$d"

# 3. singleton blocker blocks even though unconfirmed
d=$root/blocker
sample "$d" 1 "$(finding blocker src/a.py 5 5 'Funds credited twice')"
sample "$d" 2
sample "$d" 3
run "$d" 3
check "singleton blocker is flagged" '.blocker_singletons==1 and .confirmed==0' "$d"
check "singleton blocker means not happy" '.happy==false' "$d"

# 4. minors never confirm, even with full agreement
d=$root/minor
for n in 1 2 3; do sample "$d" $n "$(finding minor src/a.py 7 7 'Misleading variable name here')"; done
run "$d" 3
check "unanimous minor stays unconfirmed" '.clusters[0].votes==3 and .confirmed==0 and .happy==true' "$d"

# 5. different files or distant lines never merge
d=$root/distinct
sample "$d" 1 "$(finding major src/a.py 10 10 'Null deref in parser')" "$(finding major src/a.py 200 200 'Null deref in parser')"
sample "$d" 2 "$(finding major src/b.py 10 10 'Null deref in parser')"
sample "$d" 3
run "$d" 3
check "same title on other file or far lines stays separate" '(.clusters|length)==3 and all(.clusters[]; .votes==1)' "$d"

# 6. one sample never votes twice for one cluster
d=$root/dup
sample "$d" 1 "$(finding major src/a.py 10 10 'Off by one in loop')" "$(finding major src/a.py 10 11 'Off by one in loop bound')"
sample "$d" 2
sample "$d" 3
run "$d" 3
check "two findings from one sample count as one vote each in separate clusters" 'all(.clusters[]; .votes==1) and .confirmed==0' "$d"

# 7. malformed outputs: tolerated while >= 2 samples succeed
d=$root/malformed
sample "$d" 1 "$(finding major src/a.py 10 10 'Off by one in loop')"
sample "$d" 2 "$(finding major src/a.py 10 10 'Off by one in loop')"
printf 'I could not finish this review {{{' > "$d/sample-3.out"
run "$d" 3
check_exit "invalid JSON sample does not fail the run when 2 succeed" "$d" 0
check "failed sample is reported and the rest merge" '.ok==2 and (.failed|length)==1 and .failed[0].sample==3 and .confirmed==1' "$d"

# 8. fewer than 2 usable samples: non-zero exit
d=$root/toofew
sample "$d" 1 "$(finding major src/a.py 10 10 'Off by one in loop')"
printf 'nope' > "$d/sample-2.out"; : > "$d/sample-3.out"
run "$d" 3
check_exit "one usable sample exits non-zero" "$d" 2
check "run is invalid and not happy" '.valid==false and .happy==false and (.failed|length)==2' "$d"

# 9. missing sample file counts as failed
d=$root/missing
sample "$d" 1 "$(finding major src/a.py 10 10 'Off by one in loop')"
sample "$d" 2 "$(finding major src/a.py 10 10 'Off by one in loop')"
run "$d" 3
check "absent output file is a failed sample" '.ok==2 and .failed[0].sample==3' "$d"

# 10. reviewer wraps JSON in prose and a code fence
d=$root/fenced; mkdir -p "$d"
printf 'Here is my review.\n```json\n%s\n```\nDone.\n' '{"summary":"s","findings":[{"severity":"major","file":"./src/a.py","line_start":"4","line_end":"5","title":"Off by one in loop","evidence":"x"}]}' > "$d/sample-1.out"
printf '{"findings":[]}' > "$d/sample-2.out"
run "$d" 2
check "fenced JSON is extracted, string line numbers and ./ path normalized" '.ok==2 and .clusters[0].file=="src/a.py" and .clusters[0].line_start==4' "$d"
check "K=2 threshold is 1, so a lone major is confirmed" '.threshold==1 and .confirmed==1' "$d"

# 11. findings missing required fields are dropped and counted
d=$root/dropped
sample "$d" 1 '{"severity":"critical","file":"a.py","line_start":1,"title":"bad severity"}' '{"severity":"major","title":"no file","line_start":1}' "$(finding major src/a.py 3 3 'Kept finding')"
sample "$d" 2
run "$d" 2
check "invalid findings are dropped, valid kept" '.dropped_findings==2 and (.clusters|length)==1' "$d"

# 12. clean run
d=$root/clean
for n in 1 2 3; do sample "$d" $n; done
run "$d" 3
check "no findings is happy" '.happy==true and .clusters==[] and .ok==3' "$d"

# 13. K=5 threshold and renderers
d=$root/five
for n in 1 2 3; do sample "$d" $n "$(finding major src/a.py 10 10 'Off by one in loop')"; done
sample "$d" 4; sample "$d" 5
run "$d" 5
check "K=5 needs 3 votes" '.threshold==3 and .confirmed==1' "$d"
if "$merge" "$d" --samples 5 | grep -q 'confirmed' && "$merge" "$d" --samples 5 --markdown | grep -q '3/5'; then
  pass=$((pass + 1)); echo "ok   table and markdown render agreement counts"
else fail=$((fail + 1)); echo "FAIL renderers"; fi

echo "passed $pass, failed $fail (fixtures in $root)"
[[ $fail -eq 0 ]]
