/* 参考报表附件对照。导出中心和共治的导出授权都要摆这张表：申请人得先知道
   这一份 ZIP 解出来是哪四个文件、哪些栏系统填、哪些栏还得手工补。 */

import { Note, Sub, Table, THead, TRow } from '@/components/ui'

/* 与参考模板一致：附件1–2合用一份 Word，附件3一份 Word，附件4、5各一份 Excel。 */
const COLLEGE_FILES: { file: string; format: string; filled: string; manual: string }[] = [
  {
    file: '附件1-2 评分登记表与申报名册',
    format: 'Word · docx',
    filled: '评分登记表按学号排列全班，申报名册列出奖学金和三好学生。填好折算分、排名、等级、人数和班级信息，按页排好表头和签章位置',
    manual: '身份证号、银行卡号、备注、辅导员签名和学院盖章',
  },
  {
    file: '附件3 名单公示',
    format: 'Word · docx',
    filled: '公示正文、各档人数、奖学金和三好名单，姓名每行 7 人；保留公示照片页',
    manual: '公示日期、学院电话、签名、盖章和实际公示照片',
  },
  {
    file: '附件4 获奖者信息采集表',
    format: 'Excel · xlsx',
    filled: '按参考模板填好学院、年级、专业、学号、姓名、奖学金等级，保留表格格式和注意事项',
    manual: '获奖金额、银行卡号、开户行、身份证号',
  },
  {
    file: '附件5 证书打印信息录入模板',
    format: 'Excel · xlsx（2 张表）',
    filled: '保留参考模板的「综合素质奖学金」「三好学生」两张表，填好身份信息、等级、分数和排名',
    manual: '核对身份信息，补齐缺失的性别等资料',
  },
]

export function CollegeExportGuide() {
  return (
    <div style={{ paddingBottom: 30 }}>
      <Sub title="参考报表附件对照" note="解压后 4 个文件，与参考模板一致" />
      <Table cols="minmax(130px,1fr) minmax(120px,1fr) minmax(240px,2.2fr) minmax(150px,1.3fr)">
        <THead cells={['文件', '格式', '系统填', '需手工补']} />
        {COLLEGE_FILES.map((s) => (
          <TRow
            key={s.file}
            cells={[
              <span key="a" style={{ color: 'var(--fg)', fontWeight: 500 }}>{s.file}</span>,
              <span key="b" style={{ fontSize: 12, color: 'var(--fg3)' }}>{s.format}</span>,
              <span key="c" style={{ fontSize: 12, color: 'var(--fg3)', textWrap: 'pretty' }}>{s.filled}</span>,
              <span key="d" style={{ fontSize: 12, color: 'var(--fg3)', textWrap: 'pretty' }}>{s.manual}</span>,
            ]}
          />
        ))}
      </Table>
      <div style={{ marginTop: 12 }}>
        <Note>
          Word 可直接编辑、打印；Excel 保留参考模板，长数字列已设为文本格式。学院名、教务班级名和评定学年在「时间窗口」里补。这里生成本班材料，正式报送前仍需学院汇总、核对与签章。
        </Note>
      </div>
    </div>
  )
}
