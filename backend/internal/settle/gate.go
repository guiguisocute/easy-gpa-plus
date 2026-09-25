package settle

import "time"

type GateInput struct {
	StudentCount           int
	SealedCount            int
	WindowClose            time.Time
	Now                    time.Time
	UnfinalizedReviews     int
	PendingClassifications int
	BlindAuditComplete     bool
	ConfirmedCount         int
	PendingConflicts       int
	GPAImportedCount       int
	Forced                 bool
}

type GateCondition struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type Gate struct {
	Open       bool            `json:"open"`
	Forced     bool            `json:"forced"`
	Conditions []GateCondition `json:"conditions"`
}

func EvaluateGate(in GateInput) Gate {
	sealed := in.StudentCount > 0 && in.SealedCount >= in.StudentCount
	closed := !in.Now.Before(in.WindowClose)
	conditions := []GateCondition{
		{Key: "sealed", Label: "全员封存或窗口截止", OK: sealed || closed, Detail: countDetail(in.SealedCount, in.StudentCount, closed)},
		{Key: "allReviewsFinal", Label: "单项全部定分", OK: in.UnfinalizedReviews == 0, Detail: pendingDetail(in.UnfinalizedReviews, "条申报未定分")},
		{Key: "classificationResolved", Label: "分类建议全部处理", OK: in.PendingClassifications == 0, Detail: pendingDetail(in.PendingClassifications, "条分类建议待处理")},
		{Key: "noConflict", Label: "无待终裁冲突", OK: in.PendingConflicts == 0, Detail: conflictDetail(in.PendingConflicts)},
		{Key: "gpaImported", Label: "专业素质分已导入", OK: in.StudentCount > 0 && in.GPAImportedCount >= in.StudentCount, Detail: countDetail(in.GPAImportedCount, in.StudentCount, false)},
	}
	open := true
	for _, condition := range conditions {
		open = open && condition.OK
	}
	return Gate{Open: open || in.Forced, Forced: in.Forced, Conditions: conditions}
}

func pendingDetail(count int, suffix string) string {
	return itoa(count) + " " + suffix
}

func blindAuditDetail(complete bool) string {
	if complete {
		return "已完成"
	}
	return "尚未完成或批次已失效"
}

func countDetail(done, total int, closed bool) string {
	if closed {
		return "窗口已截止"
	}
	return itoa(done) + " / " + itoa(total)
}

func conflictDetail(count int) string {
	if count == 0 {
		return "0 条待终裁"
	}
	return itoa(count) + " 条待终裁"
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
