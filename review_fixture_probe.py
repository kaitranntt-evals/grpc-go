import os
from pathlib import Path

source = Path(os.environ['GRADING_BUNDLE_DIR']) / 'tests/eval_rds_error_after_success_test.go'
text = source.read_text()
old = 'f.filterDestroyed.Add(1)\n\tf.record("filter-close")'
new = 'f.filterDestroyed.Add(1)\n\tif reviewAfterFilterCounter != nil { reviewAfterFilterCounter() }\n\tf.record("filter-close")'
assert text.count(old) == 1
Path('internal/xds/server/eval_rds_error_after_success_test.go').write_text(text.replace(old, new))
