// Package roles 根据检测到的工具给出默认的角色分配。
//
// 规则（见 docs/DESIGN.md §3）：
//  1. 只有一个可用工具时，它承担全部角色。
//     只有已安装且有非交互调用模板的 CLI 工具参与分配。
//  2. 多个工具时，能力评分最高的工具担任"架构与项目管理"（lead）。
//  3. 其余工具按"擅长方向匹配度 + 能力评分"依次认领开发、前端/视觉、测试、评审。
//  4. 工具不够分时：评审回落给 lead（它负责合并把关）；其余角色交给最匹配的工具兼任。
//     多出来的工具作为额外开发者并行接任务。
package roles

import (
	"slices"
	"sort"

	"github.com/luckySpro/AutoCoder/internal/toolscan"
)

// Role 是项目中的一个职责。
type Role string

const (
	Lead      Role = "lead"      // 架构与项目管理：架构设计、功能/视觉拆分、进度汇总与集成
	Developer Role = "developer" // 开发：实现后端/通用功能
	Frontend  Role = "frontend"  // 前端/视觉：界面与交互实现
	Tester    Role = "tester"    // 测试：编写与执行测试、回归验证
	Reviewer  Role = "reviewer"  // 评审：代码审查、合并前把关
)

// Info 描述角色。
type Info struct {
	ID          Role   `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Strength    string `json:"strength"` // 与工具 strengths 对应的匹配方向
}

// All 是按认领优先级排列的角色列表（lead 永远第一个）。
var All = []Info{
	{Lead, "架构与项目管理", "负责架构设计、功能与视觉拆分、任务派发、进度汇总与分支集成", "architecture"},
	{Developer, "开发", "认领功能任务，在独立分支与工作区中实现并提交", "coding"},
	{Frontend, "前端/视觉", "实现界面、交互与视觉稿对应的任务", "frontend"},
	{Tester, "测试", "为任务编写测试、执行回归，并向项目管理汇报质量结果", "testing"},
	{Reviewer, "评审", "合并前审查分支差异，提出修改意见", "review"},
}

// Assignment 是某个工具被分配的角色。
type Assignment struct {
	ToolID   string `json:"toolId"`
	ToolName string `json:"toolName"`
	Roles    []Role `json:"roles"`
}

// Suggest 返回默认分配；未安装、已禁用或不可调度的工具会被忽略。
func Suggest(detected []toolscan.Detection) []Assignment {
	var avail []toolscan.Detection
	for _, d := range detected {
		if d.Schedulable() && !d.Disabled {
			avail = append(avail, d)
		}
	}
	if len(avail) == 0 {
		return nil
	}
	sort.SliceStable(avail, func(a, b int) bool { return avail[a].Capability > avail[b].Capability })

	out := make([]Assignment, len(avail))
	for i, d := range avail {
		out[i] = Assignment{ToolID: d.ID, ToolName: d.Name}
	}
	if len(avail) == 1 {
		for _, r := range All {
			out[0].Roles = append(out[0].Roles, r.ID)
		}
		return out
	}

	out[0].Roles = []Role{Lead}
	rest := avail[1:]
	taken := make([]bool, len(rest))
	for _, r := range All[1:] {
		best := -1
		for i, d := range rest {
			if taken[i] {
				continue
			}
			if best == -1 || score(d, r) > score(rest[best], r) {
				best = i
			}
		}
		if best != -1 {
			taken[best] = true
			out[best+1].Roles = []Role{r.ID}
			continue
		}
		// 人手不足：找最匹配的工具兼任。
		owner := 0
		if r.ID != Reviewer {
			for i, d := range rest {
				if owner == 0 || score(d, r) > score(rest[owner-1], r) {
					owner = i + 1
				}
			}
		}
		out[owner].Roles = append(out[owner].Roles, r.ID)
	}
	for i := range rest {
		if !taken[i] {
			out[i+1].Roles = []Role{Developer}
		}
	}
	return out
}

// score：擅长方向命中加 100 分，再叠加能力评分。
func score(d toolscan.Detection, r Info) int {
	s := d.Capability
	if slices.Contains(d.Strengths, r.Strength) {
		s += 100
	}
	return s
}
