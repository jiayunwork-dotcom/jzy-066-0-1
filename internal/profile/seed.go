package profile

import "stefan-service/internal/stefan"

// DefaultProfiles 返回服务预置工况档。
//
// ice-wall-minus20：纯水冰从 0℃ 一侧被 −20℃ 冷壁持续冷却，
// 一小时凝固厚度约 3.16 cm（厘米量级），拉起服务即可核对。
func DefaultProfiles() []Profile {
	return []Profile{
		{
			Name:        "ice-wall-minus20",
			Description: "纯水冰，壁温 -20℃，凝固点 0℃；1 小时凝固厚度约 3.16 cm",
			Params: stefan.Params{
				C:     2100,    // J/(kg·K)
				Lf:    334e3,   // J/kg
				K:     2.22,    // W/(m·K)
				Alpha: 1.15e-6, // m²/s
				Tf:    0,
				Tw:    -20,
			},
		},
	}
}
