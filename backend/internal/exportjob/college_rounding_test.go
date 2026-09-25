package exportjob

import "testing"

func TestCollegeRoundHalfUp(t *testing.T) {
	for _, tc := range []struct{ value, want float64 }{
		{85.816, 85.82}, {85.814, 85.81}, {1.005, 1.01}, {0.145, 0.15},
		{1.0049, 1.00}, {0, 0}, {-1.005, -1.01},
	} {
		if got := round2(tc.value); got != tc.want {
			t.Errorf("round2(%v) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestCollegeRowsRoundEachValueIndependently(t *testing.T) {
	data := collegeFixture()
	rows := collegeRows(data)
	for index, want := range []struct{ major, moral, practice, health, total, conduct float64 }{
		{58.65, 9.03, 9.13, 9.00, 85.82, 27.17},
		{48.07, 7.55, 6.12, 7.10, 68.83, 20.77},
	} {
		row := rows[index]
		for key, score := range map[string]float64{"major": want.major, "moral": want.moral, "practice": want.practice, "health": want.health} {
			if row.Weighted[key] != score {
				t.Errorf("row %d %s = %v, want %v", index, key, row.Weighted[key], score)
			}
		}
		if row.Total != want.total || row.Conduct != want.conduct {
			t.Errorf("row %d total/conduct = %v/%v, want %v/%v", index, row.Total, row.Conduct, want.total, want.conduct)
		}
		if row.TotalScore != data.Rows[index].TotalScore || row.ClassRank != data.Rows[index].ClassRank || row.AwardTier != data.Rows[index].AwardTier {
			t.Fatal("report rounding changed the settlement snapshot")
		}
	}
}

func TestCollegeWeightedUsesDecimalProductsBeforeRounding(t *testing.T) {
	data := collegeFixture()
	data.Config.Weights = map[string]float64{"major": 0.5, "moral": 0.5, "practice": 0, "health": 0}
	data.Rows = data.Rows[:1]
	data.Rows[0].CategoryScores = map[string]float64{"major": 0.29, "moral": 0.29}
	data.Rows[0].TotalScore = 0.29
	row := collegeRows(data)[0]
	if row.Weighted["major"] != 0.15 || row.Weighted["moral"] != 0.15 || row.Conduct != 0.15 || row.Total != 0.29 {
		t.Fatalf("0.29 × 0.5 must round to 0.15 independently: %#v", row)
	}
}

func TestCollegeWordAndExcelUseTheSameRoundedScores(t *testing.T) {
	data := collegeFixture()
	values := collegeDocumentValues(data, collegeRows(data))
	for index, want := range [][]string{
		{"58.65", "9.03", "9.13", "9.00", "85.82"},
		{"48.07", "7.55", "6.12", "7.10", "68.83"},
	} {
		for col, value := range want {
			if got := values.OnePages[0].Rows[index][2+col*2]; got != value {
				t.Errorf("Word attachment 1 row %d score %d = %s, want %s", index, col, got, value)
			}
		}
	}
	files := collegeTestFiles(t, data)
	book := collegeTestBook(t, files, 3)
	for cell, want := range map[string]string{"K4": "85.82", "M4": "58.65", "O4": "27.17", "K5": "68.83", "M5": "48.07", "O5": "20.77"} {
		collegeCell(t, book, "综合素质奖学金", cell, want)
	}
	for cell, want := range map[string]string{"J4": "85.82", "L4": "58.65", "N4": "9.03", "P4": "9.13", "R4": "9.00"} {
		collegeCell(t, book, "三好学生", cell, want)
	}
	for index, want := range []string{"27.17", "20.77"} {
		if got := values.TwoPages[0].Rows[index][3]; got != want {
			t.Errorf("Word attachment 2 row %d conduct = %s, want %s", index, got, want)
		}
	}
}
