package kit

import (
	"sync"
)



var Byte128 = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 128)
		return &buf
	},
}

var Byte23 = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 23)
		return &buf
	},
}

var Byte17 = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 17)
		return &buf
	},
}

var Byte7 = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 7)
		return &buf
	},
}

var Byte3 = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 3)
		return &buf
	},
}
