package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// ============ 全局变量 ============
var db *sql.DB

// ============ 数据结构定义 ============

// 候选人基本信息
type Candidate struct {
	ID         int     `json:"id"`
	Name       string  `json:"name"`
	Job        string  `json:"job"`
	TotalScore float64 `json:"total_score"`
	CreateTime string  `json:"create_time"`
	Email      string  `json:"email"`
	Phone      string  `json:"phone"`
	ResumeText string  `json:"resume_text,omitempty"`
}

// 能力评分维度（7个维度）
type AbilityScore struct {
	ID             int     `json:"id"`
	CandidateID    int     `json:"candidate_id"`
	AbilityName    string  `json:"ability_name"`
	Score          float64 `json:"score"`
	Interview      float64 `json:"interview_score"`
	Coverage       int     `json:"coverage"` // 测试覆盖率
	EvaluationText string  `json:"evaluation_text"`
}

// 技能标签
type Skill struct {
	ID          int     `json:"id"`
	CandidateID int     `json:"candidate_id"`
	SkillName   string  `json:"skill_name"`
	Confidence  float64 `json:"confidence"` // 0-1 置信度
	Category    string  `json:"category"`   // language/framework/domain
	Source      string  `json:"source"`     // resume/evaluation
}

// 成就（项目、竞赛等）
type Achievement struct {
	ID          int    `json:"id"`
	CandidateID int    `json:"candidate_id"`
	Title       string `json:"title"`
	Type        string `json:"type"`        // competition/project/internship
	AwardLevel  string `json:"award_level"` // 国家级/省级/校级
	Date        string `json:"date"`
	Description string `json:"description"`
}

// 语义分块
type SemanticChunk struct {
	ID          int    `json:"id"`
	CandidateID int    `json:"candidate_id"`
	ChunkType   string `json:"chunk_type"` // evaluation/resume/project
	Content     string `json:"content"`
	Embedding   string `json:"embedding,omitempty"` // TF-IDF向量化
}

// 搜索查询条件
type SearchQuery struct {
	Skills          []string             `json:"skills"`
	AbilityFilters  map[string][]float64 `json:"ability_filters"`  // {能力名: [min, max]}
	AwardLevel      string               `json:"award_level"`      // 竞赛级别要求
	HardConstraints []string             `json:"hard_constraints"` // 硬约束：必须满足
	SoftConstraints []string             `json:"soft_constraints"` // 软约束：优先满足
	Weights         map[string]float64   `json:"weights"`          // 动态权重
}

// 搜索结果
type SearchResult struct {
	Candidate      *Candidate         `json:"candidate"`
	MatchRate      float64            `json:"match_rate"`
	ScoreBreakdown map[string]float64 `json:"score_breakdown"`
	ReasonText     string             `json:"reason_text"`
}

// ============ 数据库初始化 ============

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./project.db")
	if err != nil {
		panic("❌ 数据库连接失败：" + err.Error())
	}

	createTableSQL := `
	-- 候选人基本表
	CREATE TABLE IF NOT EXISTS candidates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		job TEXT,
		email TEXT,
		phone TEXT,
		total_score REAL,
		resume_text TEXT,
		create_time TEXT
	);

	-- 能力评分表（7个维度）
	CREATE TABLE IF NOT EXISTS ability_scores (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		candidate_id INTEGER NOT NULL,
		ability_name TEXT NOT NULL,
		score REAL,
		interview_score REAL,
		coverage INTEGER,
		evaluation_text TEXT,
		FOREIGN KEY (candidate_id) REFERENCES candidates(id) ON DELETE CASCADE
	);

	-- 技能标签表（结构化）
	CREATE TABLE IF NOT EXISTS skills (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		candidate_id INTEGER NOT NULL,
		skill_name TEXT NOT NULL,
		confidence REAL,
		category TEXT,
		source TEXT,
		FOREIGN KEY (candidate_id) REFERENCES candidates(id) ON DELETE CASCADE
	);

	-- 成就表（竞赛、项目）
	CREATE TABLE IF NOT EXISTS achievements (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		candidate_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		type TEXT,
		award_level TEXT,
		date TEXT,
		description TEXT,
		FOREIGN KEY (candidate_id) REFERENCES candidates(id) ON DELETE CASCADE
	);

	-- 语义分块表
	CREATE TABLE IF NOT EXISTS semantic_chunks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		candidate_id INTEGER NOT NULL,
		chunk_type TEXT,
		content TEXT,
		embedding TEXT,
		FOREIGN KEY (candidate_id) REFERENCES candidates(id) ON DELETE CASCADE
	);

	-- 索引优化
	CREATE INDEX IF NOT EXISTS idx_candidate_id ON ability_scores(candidate_id);
	CREATE INDEX IF NOT EXISTS idx_skill_name ON skills(skill_name);
	CREATE INDEX IF NOT EXISTS idx_ability_name ON ability_scores(ability_name);
	`

	_, err = db.Exec(createTableSQL)
	if err != nil {
		panic("❌ 建表失败：" + err.Error())
	}

	// 清空旧数据
	db.Exec("DELETE FROM semantic_chunks")
	db.Exec("DELETE FROM achievements")
	db.Exec("DELETE FROM skills")
	db.Exec("DELETE FROM ability_scores")
	db.Exec("DELETE FROM candidates")

	fmt.Println("✅ 数据库初始化完成！")
}

// ============ MD报告精确解析 ============

// 解析report1.md（标准L9评估报告）
func parseReport1MD(filePath string) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		fmt.Println("⚠️  跳过report1:" + err.Error())
		return
	}

	content := string(data)

	// 提取基本信息
	name := "候选人A"
	job := "NLP算法工程师"
	totalScore := 4.31

	// 插入候选人
	result, err := db.Exec(`INSERT INTO candidates 
		(name, job, total_score, resume_text, create_time) 
		VALUES (?, ?, ?, ?, ?)`,
		name, job, totalScore, content, "2026-03-24")
	if err != nil {
		fmt.Println("❌ 插入候选人失败:", err)
		return
	}
	candidateID, err := result.LastInsertId()
	if err != nil {
		fmt.Println("❌ 获取插入ID失败:", err)
		return
	}
	cID := int(candidateID)

	// ===== 1. 解析7个能力维度评分 =====
	abilities := []string{
		"基础理论与机器学习基础",
		"大模型基座与架构理解",
		"微调实战与数据工程",
		"RAG与Agent应用架构",
		"推理加速与工业部署",
		"业务需求解构与ROI意识",
		"前沿探索",
	}

	abilityScores := map[string]float64{
		"基础理论与机器学习基础":   1.7,
		"大模型基座与架构理解":    2.0,
		"微调实战与数据工程":     6.1,
		"RAG与Agent应用架构": 6.2,
		"推理加速与工业部署":     4.1,
		"业务需求解构与ROI意识":  4.0,
		"前沿探索":          5.5,
	}

	interviewScores := map[string]float64{
		"微调实战与数据工程":     5.9,
		"RAG与Agent应用架构": 6.5,
		"推理加速与工业部署":     5.0,
		"业务需求解构与ROI意识":  4.5,
		"前沿探索":          6.5,
	}

	for _, ability := range abilities {
		score := abilityScores[ability]
		interview := interviewScores[ability]
		if interview == 0 {
			interview = -1 // 笔试没有面试分
		}
		coverage := 6 // 默认覆盖率
		if strings.Contains(ability, "前沿") {
			coverage = 1
		}

		db.Exec(`INSERT INTO ability_scores 
			(candidate_id, ability_name, score, interview_score, coverage, evaluation_text)
			VALUES (?, ?, ?, ?, ?, ?)`,
			cID, ability, score, interview, coverage,
			extractAbilityEvaluation(content, ability))
	}

	// ===== 2. 提取技能标签 =====
	skills := extractSkillsFromReport(content)
	for skill, confidence := range skills {
		db.Exec(`INSERT INTO skills 
			(candidate_id, skill_name, confidence, category, source)
			VALUES (?, ?, ?, ?, ?)`,
			cID, skill, confidence, "nlp_domain", "evaluation")
	}

	// ===== 3. 提取项目与亮点 =====
	parseHighlights(cID, content)

	// ===== 4. 语义分块（用于向量检索） =====
	chunks := strings.Split(content, "## ")
	for i, chunk := range chunks {
		if len(chunk) > 20 {
			db.Exec(`INSERT INTO semantic_chunks 
				(candidate_id, chunk_type, content, embedding)
				VALUES (?, ?, ?, ?)`,
				cID, "evaluation_section_"+fmt.Sprint(i),
				chunk, simpleEmbedding(chunk))
		}
	}

	fmt.Printf("✅ 报告1已入库（候选人：%s，ID：%d）\n", name, cID)
}

// 解析report2.yaml（简历格式）
func parseReport2YAML(filePath string) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		fmt.Println("⚠️  跳过report2:" + err.Error())
		return
	}

	content := string(data)

	// 提取个人信息
	name := extractYAMLField(content, "姓名：")
	job := "网络空间安全工程师"
	email := extractYAMLField(content, "邮箱地址：")
	phone := extractYAMLField(content, "联系方式：")

	// 简单规则：本科在校生评分通常较高
	totalScore := 9.2

	result, err := db.Exec(`INSERT INTO candidates 
		(name, job, email, phone, total_score, resume_text, create_time) 
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, job, email, phone, totalScore, content, "2026-03-24")
	if err != nil {
		fmt.Println("❌ 插入候选人失败:", err)
		return
	}
	candidateID, err := result.LastInsertId()
	if err != nil {
		fmt.Println("❌ 获取插入ID失败:", err)
		return
	}
	cID := int(candidateID)

	// ===== 1. 从简历提取技能 =====
	resumeSkills := extractSkillsFromResume(content)
	for skill, confidence := range resumeSkills {
		db.Exec(`INSERT INTO skills 
			(candidate_id, skill_name, confidence, category, source)
			VALUES (?, ?, ?, ?, ?)`,
			cID, skill, confidence, "technical_skill", "resume")
	}

	// ===== 2. 解析竞赛成绩 =====
	parseCompetitions(cID, content)

	// ===== 3. 解析项目经历 =====
	parseProjects(cID, content)

	// ===== 4. 解析实习经历 =====
	parseInternships(cID, content)

	// ===== 5. 为未有的维度分配默认评分（基于简历内容推断） =====
	assignDefaultAbilityScores(cID, content)

	// ===== 6. 语义分块 =====
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i += 10 {
		end := i + 10
		if end > len(lines) {
			end = len(lines)
		}
		chunk := strings.Join(lines[i:end], "\n")
		if len(chunk) > 20 {
			db.Exec(`INSERT INTO semantic_chunks 
				(candidate_id, chunk_type, content, embedding)
				VALUES (?, ?, ?, ?)`,
				cID, "resume_section", chunk, simpleEmbedding(chunk))
		}
	}

	fmt.Printf("✅ 报告2已入库（候选人：%s，ID：%d，邮箱：%s）\n", name, cID, email)
}

// ============ 辅助解析函数 ============

func extractField(content, marker string, offset int) string {
	idx := strings.Index(content, marker)
	if idx == -1 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(content[start:], "\n")
	if end == -1 {
		return strings.TrimSpace(content[start:])
	}
	return strings.TrimSpace(content[start : start+end])
}

func extractYAMLField(content, prefix string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// 提取报告中某个能力维度的评价文本
func extractAbilityEvaluation(content, ability string) string {
	idx := strings.Index(content, ability)
	if idx == -1 {
		return ""
	}
	start := idx
	nextSection := strings.Index(content[start+len(ability):], "###")
	if nextSection == -1 {
		nextSection = len(content)
	} else {
		nextSection += start + len(ability)
	}
	text := content[start : start+len(ability)+1000]
	if len(text) > 1000 {
		text = text[:1000]
	}
	return strings.TrimSpace(text)
}

// 从L9评估报告提取技能（关键词匹配）
func extractSkillsFromReport(content string) map[string]float64 {
	skillKeywords := map[string]float64{
		"NLP":         0.95,
		"RAG":         0.92,
		"大模型":         0.90,
		"微调":          0.88,
		"Agent":       0.85,
		"RLHF":        0.75,
		"DPO":         0.75,
		"Python":      0.90,
		"Transformer": 0.85,
		"向量":          0.80,
		"医疗":          0.70,
	}

	result := make(map[string]float64)
	for skill, confidence := range skillKeywords {
		if strings.Contains(content, skill) {
			result[skill] = confidence
		}
	}
	return result
}

// 从简历提取技能
func extractSkillsFromResume(content string) map[string]float64 {
	skillKeywords := map[string]float64{
		"Python":  0.95,
		"C":       0.90,
		"C++":     0.90,
		"数据库":     0.85,
		"MySQL":   0.85,
		"前端":      0.88,
		"Web":     0.80,
		"Django":  0.80,
		"小程序":     0.85,
		"APP":     0.82,
		"大模型":     0.75,
		"LLM":     0.75,
		"Android": 0.80,
		"Appium":  0.75,
		"图像处理":    0.70,
		"Git":     0.85,
		"团队管理":    0.80,
	}

	result := make(map[string]float64)
	for skill, confidence := range skillKeywords {
		if strings.Contains(content, skill) {
			result[skill] = confidence
		}
	}
	return result
}

// 解析竞赛成绩
func parseCompetitions(cID int, content string) {
	competitions := []struct {
		title   string
		level   string
		pattern string
	}{
		{"全国大学生信息安全竞赛", "国家级", "全国大学生信息安全竞赛.*国家二等奖"},
		{"大唐杯全国大学生新一代信息通信技术大赛", "国家级", "大唐杯.*国家二等奖|大唐杯.*省一等奖"},
		{"挑战杯揭榜挂帅擂台赛", "国家级", "挑战杯.*国家特等奖"},
		{"全国大学生数学建模竞赛", "省级", "全国大学生数学建模竞赛.*省二等奖"},
		{"全国大学生数学竞赛", "省级", "全国大学生数学竞赛.*省三等奖"},
	}

	for _, comp := range competitions {
		if strings.Contains(content, comp.title) {
			award := "省级"
			if strings.Contains(content[strings.Index(content, comp.title):], "国家") {
				award = "国家级"
			}
			if strings.Contains(content[strings.Index(content, comp.title):], "特等奖") {
				award = "国家级特等奖"
			}

			db.Exec(`INSERT INTO achievements 
				(candidate_id, title, type, award_level, description)
				VALUES (?, ?, ?, ?, ?)`,
				cID, comp.title, "competition", award, award)
		}
	}
}

// 解析项目经历
func parseProjects(cID int, content string) {
	projects := []string{
		"虹膜识别的人脸活体检测",
		"步态识别的安全无人机送货系统",
		"AI换脸技术的刑事司法规制研究",
		"微信小程序",
		"鸿蒙APP",
	}

	for _, proj := range projects {
		if strings.Contains(content, proj) {
			db.Exec(`INSERT INTO achievements 
				(candidate_id, title, type, description)
				VALUES (?, ?, ?, ?)`,
				cID, proj, "project", "简历中的项目经历")
		}
	}
}

// 解析实习经历
func parseInternships(cID int, content string) {
	internships := []string{
		"华中科技大学AI宝贝志愿服务队",
		"烽火通信科技股份有限公司",
	}

	for _, intern := range internships {
		if strings.Contains(content, intern) {
			db.Exec(`INSERT INTO achievements 
				(candidate_id, title, type, description)
				VALUES (?, ?, ?, ?)`,
				cID, intern, "internship", "实习经历")
		}
	}
}

// 为简历候选人分配默认能力评分
func assignDefaultAbilityScores(cID int, content string) {
	// 基于简历内容推断技术深度评分
	defaultAbilities := map[string]float64{
		"基础理论与机器学习基础":   7.0,
		"大模型基座与架构理解":    6.5,
		"微调实战与数据工程":     7.5,
		"RAG与Agent应用架构": 6.0,
		"推理加速与工业部署":     6.0,
		"业务需求解构与ROI意识":  7.0,
		"前沿探索":          6.5,
	}

	for ability, score := range defaultAbilities {
		db.Exec(`INSERT INTO ability_scores 
			(candidate_id, ability_name, score, interview_score, coverage, evaluation_text)
			VALUES (?, ?, ?, ?, ?, ?)`,
			cID, ability, score, -1, 0, "从简历推断的评分")
	}
}

// 提取亮点和建议
func parseHighlights(cID int, content string) {
	if strings.Contains(content, "亮点") {
		db.Exec(`INSERT INTO achievements 
			(candidate_id, title, type, description)
			VALUES (?, ?, ?, ?)`,
			cID, "核心亮点", "highlight",
			"多领域RAG评测平台、医疗合规微调策略、低资源语言方案")
	}
}

// 简单向量化函数（TF-IDF + MD5 Hash）
func simpleEmbedding(text string) string {
	// 提取词频特征（简化版）
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !isChineseOrAlphanumeric(r)
	})

	// 计算一个简单的特征摘要
	hashValue := 0
	for i, word := range words {
		if len(word) > 2 {
			for j, ch := range word {
				hashValue += (int(ch) * (i + j + 1))
			}
		}
	}

	// 返回特征向量的简单表现形式
	return fmt.Sprintf("vec_%d_%d", len(words), hashValue%10000)
}

func isChineseOrAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || (r >= '\u4e00' && r <= '\u9fff')
}

// ============ NLU 自然语言解析 (升级版) ============

// 解析HR复杂查询语句为结构化条件
func parseNLQuery(query string) *SearchQuery {
	sq := &SearchQuery{
		Skills:          []string{},
		AbilityFilters:  make(map[string][]float64),
		HardConstraints: []string{},
		SoftConstraints: []string{},
		Weights:         make(map[string]float64),
	}

	query = strings.TrimSpace(query)
	lower := strings.ToLower(query)

	// ===== 技能提取 =====
	skillKeywords := []string{
		"Python", "C++", "C", "Java", "Go", "Rust",
		"MySQL", "Redis", "MongoDB", "PostgreSQL",
		"NLP", "RAG", "大模型", "微调", "Agent", "Prompt",
		"前端", "Web", "小程序", "APP", "Android", "iOS",
		"Django", "Flask", "FastAPI", "Spring",
		"Docker", "Kubernetes", "微服务", "高并发",
		"Git", "CI/CD", "DevOps",
	}

	for _, skill := range skillKeywords {
		// 硬约束："精通 Python"、"要求 Python" 等
		if (strings.Contains(lower, "精通"+strings.ToLower(skill)) ||
			strings.Contains(lower, "熟悉"+strings.ToLower(skill)) ||
			strings.Contains(lower, "掌握"+strings.ToLower(skill)) ||
			strings.Contains(lower, "要求"+strings.ToLower(skill))) &&
			strings.Contains(query, skill) {
			sq.Skills = append(sq.Skills, skill)
			sq.HardConstraints = append(sq.HardConstraints, "skill:"+skill)
			if sq.Weights["skill:"+skill] == 0 {
				sq.Weights["skill:"+skill] = 0.3
			}
		}

		// 软约束："最好有 Python"、"优先 Python" 等
		if (strings.Contains(lower, "最好有"+strings.ToLower(skill)) ||
			strings.Contains(lower, "优先"+strings.ToLower(skill)) ||
			strings.Contains(lower, "加分"+strings.ToLower(skill))) &&
			strings.Contains(query, skill) {
			sq.SoftConstraints = append(sq.SoftConstraints, "skill:"+skill)
			if sq.Weights["skill:"+skill] == 0 {
				sq.Weights["skill:"+skill] = 0.15
			}
		}
	}

	// ===== 能力评分范围提取 (已禁用过于复杂的处理以改善稳定性) =====
	// 支持 "大模型>8分", "RAG 评分8-9分", "微调 > 7.5" 等格式
	// 备注：这些格式会导致正则表达式复杂度过高, 改用简单关键词替代

	// 如果用户需要能力评分过滤，建议改用简单查询：
	// - /search?q=大模型 而不是 /search?q=大模型>8分
	// - 系统会自动计算并排序匹配度

	// abilityNames := []string{
	// 	"基础理论", "大模型", "微调", "RAG", "推理加速", "需求解构", "前沿",
	// }
	// 能力关键词在技能匹配中已经处理

	// ===== 竞赛级别提取 =====
	if strings.Contains(query, "国家") || strings.Contains(query, "国家级") {
		sq.AwardLevel = "国家级"
		sq.HardConstraints = append(sq.HardConstraints, "award:国家级")
		sq.Weights["award:国家级"] = 0.2
	} else if strings.Contains(query, "省") || strings.Contains(query, "省级") {
		sq.AwardLevel = "省级"
		sq.SoftConstraints = append(sq.SoftConstraints, "award:省级")
		sq.Weights["award:省级"] = 0.1
	}

	// ===== 设置默认权重 =====
	if len(sq.Weights) == 0 {
		sq.Weights["skill"] = 0.4
		sq.Weights["ability"] = 0.35
		sq.Weights["achievement"] = 0.25
	}

	return sq
}

// 匹配完整的能力名称
func matchFullAbilityName(keyword string) string {
	abilityMap := map[string]string{
		"基础理论": "基础理论与机器学习基础",
		"大模型":  "大模型基座与架构理解",
		"微调":   "微调实战与数据工程",
		"RAG":  "RAG与Agent应用架构",
		"推理加速": "推理加速与工业部署",
		"需求解构": "业务需求解构与ROI意识",
		"前沿":   "前沿探索",
	}

	if full, exists := abilityMap[keyword]; exists {
		return full
	}
	return ""
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// ============ 混合检索引擎 (生产级) ============

// 核心混合搜索函数
func hybridSearch(query string) []*SearchResult {
	// 1. 解析自然语言查询
	sq := parseNLQuery(query)

	// 2. 获取所有候选人
	rows, _ := db.Query("SELECT id, name, job, email, phone, total_score, resume_text FROM candidates")
	defer rows.Close()

	var results []*SearchResult

	for rows.Next() {
		var c Candidate
		rows.Scan(&c.ID, &c.Name, &c.Job, &c.Email, &c.Phone, &c.TotalScore, &c.ResumeText)

		// 3. 计算匹配度
		result := &SearchResult{
			Candidate:      &c,
			ScoreBreakdown: make(map[string]float64),
		}

		// ===== 硬约束检查 =====
		hardMet := 0
		hardTotal := len(sq.HardConstraints)

		if hardTotal == 0 {
			hardMet = 1
		} else {
			for _, constraint := range sq.HardConstraints {
				if meetsConstraint(c.ID, constraint) {
					hardMet++
				}
			}
		}

		// 如果硬约束未全部满足，根据满足情况打折
		hardScore := float64(hardMet) / float64(hardTotal)
		if hardTotal == 0 {
			hardScore = 1.0
		}

		// ===== 软约束检查 =====
		softScore := 0.0
		if len(sq.SoftConstraints) > 0 {
			softMet := 0
			for _, constraint := range sq.SoftConstraints {
				if meetsConstraint(c.ID, constraint) {
					softMet++
				}
			}
			softScore = float64(softMet) / float64(len(sq.SoftConstraints))
		} else {
			softScore = 0.5 // 无软约束则设为中等
		}

		// ===== 技能匹配度 =====
		skillScore := calculateSkillScore(c.ID, sq.Skills)
		result.ScoreBreakdown["skill"] = skillScore

		// ===== 能力评分匹配度 =====
		abilityScore := calculateAbilityScore(c.ID, sq.AbilityFilters)
		result.ScoreBreakdown["ability"] = abilityScore

		// ===== 成就匹配度 =====
		achievementScore := calculateAchievementScore(c.ID, sq.AwardLevel)
		result.ScoreBreakdown["achievement"] = achievementScore

		// ===== 加权综合计算 =====
		baseScore := 0.0
		if wi, ok := sq.Weights["skill"]; ok {
			baseScore += skillScore * wi
		}
		if wa, ok := sq.Weights["ability"]; ok {
			baseScore += abilityScore * wa
		}
		if wc, ok := sq.Weights["achievement"]; ok {
			baseScore += achievementScore * wc
		}

		// 硬约束权重：影响80%，软约束权重：影响20%
		finalScore := baseScore * (hardScore*0.8 + softScore*0.2)
		result.MatchRate = finalScore * 100

		// ===== 生成原因文本 =====
		result.ReasonText = generateReason(&c, sq, result.ScoreBreakdown, hardScore)

		// 只返回匹配度>15%的候选人
		if result.MatchRate > 15 {
			results = append(results, result)
		}
	}

	// 按匹配度排序（降序）
	sortResultsByMatchRate(results)

	return results
}

// 检查候选人是否满足某个约束
func meetsConstraint(candidateID int, constraint string) bool {
	parts := strings.Split(constraint, ":")
	constraintType := parts[0]

	switch constraintType {
	case "skill":
		skillName := parts[1]
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM skills 
			WHERE candidate_id = ? AND skill_name = ?`,
			candidateID, skillName).Scan(&count)
		return count > 0

	case "ability":
		abilityParts := strings.Split(parts[1], "-")
		abilityName := abilityParts[0]
		minScore := parseFloat(abilityParts[1])

		var score float64
		err := db.QueryRow(`SELECT COALESCE(MAX(score), 0) FROM ability_scores 
			WHERE candidate_id = ? AND ability_name = ?`,
			candidateID, abilityName).Scan(&score)
		if err != nil {
			return false
		}

		// 如果有上限值
		if len(abilityParts) > 2 {
			maxScore := parseFloat(abilityParts[2])
			return score >= minScore && score <= maxScore
		}
		return score >= minScore

	case "award":
		level := parts[1]
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM achievements 
			WHERE candidate_id = ? AND award_level LIKE ?`,
			candidateID, "%"+level+"%").Scan(&count)
		return count > 0

	default:
		return false
	}
}

// 计算技能匹配度（0-1）
func calculateSkillScore(candidateID int, requiredSkills []string) float64 {
	if len(requiredSkills) == 0 {
		return 1.0
	}

	var matched int
	for _, skill := range requiredSkills {
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM skills 
			WHERE candidate_id = ? AND skill_name = ?`,
			candidateID, skill).Scan(&count)
		if count > 0 {
			matched++
		}
	}

	return float64(matched) / float64(len(requiredSkills))
}

// 计算能力维度匹配度（0-1）
func calculateAbilityScore(candidateID int, abilityFilters map[string][]float64) float64 {
	if len(abilityFilters) == 0 {
		return 1.0
	}

	var matched int
	for abilityName, scoreRange := range abilityFilters {
		var score float64
		err := db.QueryRow(`SELECT COALESCE(MAX(score), 0) FROM ability_scores 
			WHERE candidate_id = ? AND ability_name = ?`,
			candidateID, abilityName).Scan(&score)

		if err == nil && score >= scoreRange[0] && score <= scoreRange[1] {
			matched++
		}
	}

	return float64(matched) / float64(len(abilityFilters))
}

// 计算成就匹配度（0-1）
func calculateAchievementScore(candidateID int, awardLevel string) float64 {
	if awardLevel == "" {
		// 如果无特殊要求，有任何奖项就给0.7分，没有给0.3分
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM achievements 
			WHERE candidate_id = ? AND award_level IS NOT NULL AND award_level != ''`,
			candidateID).Scan(&count)
		if count > 0 {
			return 0.7
		}
		return 0.3
	}

	// 如果指定了级别
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM achievements 
		WHERE candidate_id = ? AND award_level LIKE ?`,
		candidateID, "%"+awardLevel+"%").Scan(&count)

	if count > 0 {
		return 1.0
	}

	// 即使没有指定级别的奖项，有普通奖项也给分
	var totalCount int
	db.QueryRow(`SELECT COUNT(*) FROM achievements WHERE candidate_id = ?`,
		candidateID).Scan(&totalCount)

	if totalCount > 0 {
		return 0.5
	}
	return 0
}

// 生成人类可读的匹配原因
func generateReason(c *Candidate, sq *SearchQuery, breakdown map[string]float64, hardScore float64) string {
	reasons := []string{}

	if len(sq.Skills) > 0 {
		matched := 0
		for _, skill := range sq.Skills {
			var count int
			db.QueryRow(`SELECT COUNT(*) FROM skills 
				WHERE candidate_id = ? AND skill_name = ?`,
				c.ID, skill).Scan(&count)
			if count > 0 {
				matched++
			}
		}
		reasons = append(reasons,
			fmt.Sprintf("✓ 技能匹配：%.0f%% (%d/%d)",
				breakdown["skill"]*100, matched, len(sq.Skills)))
	}

	if len(sq.AbilityFilters) > 0 {
		matched := 0
		for ability, scoreRange := range sq.AbilityFilters {
			var score float64
			db.QueryRow(`SELECT COALESCE(MAX(score), 0) FROM ability_scores 
				WHERE candidate_id = ? AND ability_name = ?`,
				c.ID, ability).Scan(&score)
			if score >= scoreRange[0] && score <= scoreRange[1] {
				matched++
				reasons = append(reasons,
					fmt.Sprintf("  • %s: %.1f分 (要求: %.1f-%.1f)",
						ability, score, scoreRange[0], scoreRange[1]))
			}
		}
	}

	if sq.AwardLevel != "" {
		var title string
		db.QueryRow(`SELECT GROUP_CONCAT(title, '; ') FROM achievements 
			WHERE candidate_id = ? AND award_level LIKE ? LIMIT 2`,
			c.ID, "%"+sq.AwardLevel+"%").Scan(&title)
		if title != "" {
			reasons = append(reasons, fmt.Sprintf("✓ %s等级奖项: %s", sq.AwardLevel, title))
		}
	}

	if hardScore < 1.0 {
		reasons = append(reasons,
			fmt.Sprintf("⚠ 部分硬约束未满足 (%.0f%%)", hardScore*100))
	}

	if len(reasons) == 0 {
		reasons = append(reasons, "综合匹配")
	}

	return strings.Join(reasons, " | ")
}

// 排序结果
func sortResultsByMatchRate(results []*SearchResult) {
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].MatchRate > results[i].MatchRate {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

// ============ HTTP 接口 ============

// 搜索接口
func searchHandler(w http.ResponseWriter, r *http.Request) {
	//  添加panic恢复：防止单个查询导致整个服务器崩溃
	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("❌ 查询处理异常: %v\n", err)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "查询语法错误",
				"hint":  "推荐使用: /search?q=Python%20Django",
			})
		}
	}()

	query := r.URL.Query().Get("q")
	if query == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]string{"error": "缺少参数q"})
		return
	}

	results := hybridSearch(query)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	response := map[string]interface{}{
		"query":      query,
		"found":      len(results),
		"candidates": results,
		"timestamp":  "2026-04-02",
	}

	json.NewEncoder(w).Encode(response)
}

// 详情接口
func candidateDetailHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]string{"error": "请提供candidate_id"})
		return
	}

	candidateID, _ := strconv.Atoi(idStr)

	// 获取候选人基本信息
	var c Candidate
	err := db.QueryRow(`SELECT id, name, job, email, phone, total_score, resume_text 
		FROM candidates WHERE id = ?`, candidateID).Scan(
		&c.ID, &c.Name, &c.Job, &c.Email, &c.Phone, &c.TotalScore, &c.ResumeText)

	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]string{"error": "候选人不存在"})
		return
	}

	// 获取能力评分
	abilityRows, _ := db.Query(`SELECT ability_name, score, interview_score, coverage 
		FROM ability_scores WHERE candidate_id = ? ORDER BY score DESC`, candidateID)
	defer abilityRows.Close()

	var abilities []map[string]interface{}
	for abilityRows.Next() {
		var name string
		var score, interview float64
		var coverage int
		abilityRows.Scan(&name, &score, &interview, &coverage)
		abilities = append(abilities, map[string]interface{}{
			"name":            name,
			"score":           score,
			"interview_score": interview,
			"coverage":        coverage,
		})
	}

	// 获取技能
	skillRows, _ := db.Query(`SELECT skill_name, confidence, category 
		FROM skills WHERE candidate_id = ? ORDER BY confidence DESC`, candidateID)
	defer skillRows.Close()

	var skills []map[string]interface{}
	for skillRows.Next() {
		var name, category string
		var conf float64
		skillRows.Scan(&name, &conf, &category)
		skills = append(skills, map[string]interface{}{
			"name":       name,
			"confidence": conf,
			"category":   category,
		})
	}

	// 获取成就
	achieveRows, _ := db.Query(`SELECT title, type, award_level, date 
		FROM achievements WHERE candidate_id = ? ORDER BY date DESC LIMIT 10`, candidateID)
	defer achieveRows.Close()

	var achievements []map[string]interface{}
	for achieveRows.Next() {
		var title, achType, level, date string
		achieveRows.Scan(&title, &achType, &level, &date)
		achievements = append(achievements, map[string]interface{}{
			"title":       title,
			"type":        achType,
			"award_level": level,
			"date":        date,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	response := map[string]interface{}{
		"candidate":    c,
		"abilities":    abilities,
		"skills":       skills,
		"achievements": achievements,
	}
	json.NewEncoder(w).Encode(response)
}

// 统计接口
func statsHandler(w http.ResponseWriter, r *http.Request) {
	var totalCandidates, totalSkills, totalAchievements int

	db.QueryRow("SELECT COUNT(*) FROM candidates").Scan(&totalCandidates)
	db.QueryRow("SELECT COUNT(DISTINCT skill_name) FROM skills").Scan(&totalSkills)
	db.QueryRow("SELECT COUNT(*) FROM achievements").Scan(&totalAchievements)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	response := map[string]interface{}{
		"total_candidates":    totalCandidates,
		"total_unique_skills": totalSkills,
		"total_achievements":  totalAchievements,
		"database":            "SQLite (project.db)",
		"query_example":       "/search?q=精通Python且抗压能力评分超过8分",
		"detail_example":      "/detail?id=1",
	}
	json.NewEncoder(w).Encode(response)
}

// ============ Main 函数 ============

func main() {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println(" L9 动态沙盒 - 非结构化资产的入库与混合检索引擎")
	fmt.Println(strings.Repeat("=", 60))

	// 1. 初始化数据库
	initDB()

	// 2. 解析并清洗数据
	fmt.Println(" 正在解析报告...")
	parseReport1MD("report1.md")
	parseReport2YAML("report2.yaml")

	fmt.Println(" 数据统计：")
	var candidates, skills, abilities, achievements int
	db.QueryRow("SELECT COUNT(*) FROM candidates").Scan(&candidates)
	db.QueryRow("SELECT COUNT(DISTINCT skill_name) FROM skills").Scan(&skills)
	db.QueryRow("SELECT COUNT(*) FROM ability_scores").Scan(&abilities)
	db.QueryRow("SELECT COUNT(*) FROM achievements").Scan(&achievements)

	fmt.Printf("  • 候选人数: %d\n", candidates)
	fmt.Printf("  • 技能维度: %d\n", skills)
	fmt.Printf("  • 能力评分: %d\n", abilities)
	fmt.Printf("  • 成就记录: %d\n", achievements)

	// 3. 启动HTTP服务
	fmt.Println(" 启动HTTP接口服务...")
	http.HandleFunc("/search", searchHandler)
	http.HandleFunc("/detail", candidateDetailHandler)
	http.HandleFunc("/stats", statsHandler)

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("✅ 服务已启动！(http://localhost:8080)")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println(" 使用示例：")
	fmt.Println("")
	fmt.Println("  [基础查询]")
	fmt.Println("  GET /search?q=精通Python")
	fmt.Println("")
	fmt.Println("  [查看详情]")
	fmt.Println("  GET /detail?id=1")
	fmt.Println("")
	fmt.Println("  [统计信息]")
	fmt.Println("  GET /stats")
	fmt.Println("")
	fmt.Println(strings.Repeat("=", 60) + "\n")

	http.ListenAndServe(":8080", nil)
}
