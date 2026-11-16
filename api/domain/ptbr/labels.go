package ptbr

// Rótulos em português dos códigos do acervo.
var (
	Family   = map[string]string{"solido": "solid", "gota": "liquid droplet", "gas": "gas flame", "fumaca": "smoke"}
	Outcome  = map[string]string{"queimou": "burned", "extinguiu": "went out", "disrupcao": "droplet burst", "nao_acendeu": "did not ignite", "desconhecido": "not tabulated"}
	Material = map[string]string{"sibal": "Cotton fabric", "pmma": "Acrylic", "nomex": "Nomex", "silicone": "Silicone", "cera": "Wax", "delrin": "Delrin", "outro": "Other solid"}
	Tier     = map[string]string{"medido": "Measured", "delimitado": "Bounded", "parcial": "One side only", "sem_evidencia": "Uncharted"}
)
