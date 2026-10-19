package retry

import stderrors "errors"

var errNilFunc = stderrors.New("retry: nil operation function")
