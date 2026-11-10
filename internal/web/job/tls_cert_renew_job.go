package job

import (
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service/tlscert"
)

// TlsCertRenewJob renews panel-issued certificates nearing expiry and
// re-pushes current ones to nodes that missed them.
type TlsCertRenewJob struct {
	service tlscert.Service
}

func NewTlsCertRenewJob() *TlsCertRenewJob {
	return &TlsCertRenewJob{}
}

func (j *TlsCertRenewJob) Run() {
	j.service.RenewDue(time.Now())
}
