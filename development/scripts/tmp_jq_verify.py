import pathlib, re, subprocess, json, time, sys

wf = pathlib.Path('.github/workflows/run-pr-ci-tests-and-deployments.yaml').read_text()

# Extract each jq program passed via --arg dispatch_id ... '<program>' <<< "$runs_json"
progs = []
for chunk in wf.split("run_line=\"$(jq -r ")[1:]:
    body, sep, _rest = chunk.partition("' <<< \"$runs_json\")")
    if sep:
        progs.append(body.split("--argjson since \"$match_since\" ", 1)[-1])
print('extracted jq programs:', len(progs))

now = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
old = '2020-01-01T00:00:00Z'
cases = {
 'A_fresh_other_suffix_EXPECT_match': ("911", [{"id": 21, "status": "queued", "conclusion": None, "html_url": "u",
    "display_title": "Deploy msgmate-io-pr-deployment PR#911 111-1", "created_at": now}]),
 'B_old_same_deployment_EXPECT_empty': ("911", [{"id": 22, "status": "completed", "conclusion": "success", "html_url": "u",
    "display_title": "Deploy msgmate-io-pr-deployment PR#900 111-1", "created_at": old}]),
 'C_fresh_other_deployment_EXPECT_empty': ("911", [{"id": 23, "status": "in_progress", "conclusion": None, "html_url": "u",
    "display_title": "Deploy msgmate-io-staging 39445843897-3", "created_at": now}]),
 'D_exact_id_old_EXPECT_match': ("911", [{"id": 24, "status": "queued", "conclusion": None, "html_url": "u",
    "display_title": "Deploy msgmate-io-pr-deployment PR#911 39445843897-3", "created_at": old}]),
}
out = []
for name, (pr, runs) in cases.items():
    runs_json = json.dumps({"workflow_runs": runs})
    for i, prog in enumerate(progs):
        script = f'''set -u
runs_json={json.dumps(runs_json)}
DISPATCH_RUN_ID="39445843897-3"
DEPLOYMENT_NAME="msgmate-io-pr-deployment"
PR_NUMBER="{pr}"
expected_title_prefix="Deploy msgmate-io-pr-deployment"
match_since=$(( $(date +%s) - 300 ))
run_line="$(jq -r {prog}' <<< "$runs_json")"
printf 'RESULT=%s\\n' "$run_line"
'''
        r = subprocess.run(['bash', '-c', script], capture_output=True, text=True)
        got = r.stdout.strip().splitlines()[-1] if r.stdout.strip() else 'ERR:' + r.stderr.strip().splitlines()[-1][:100]
        out.append(f'{name} | progs[{i}] rc={r.returncode} {got[:70]}')
pathlib.Path('/tmp/jqverify.txt').write_text('\n'.join(out))
print('\n'.join(out))
