"""Constantes físicas, com a fonte de cada valor."""

SIGMA = 5.670374419e-8      # W m^-2 K^-4, Stefan-Boltzmann (CODATA 2018)
R_GAS = 8.314462618         # J mol^-1 K^-1 (CODATA 2018)
AU_M = 1.495978707e11       # m, unidade astronômica (IAU 2012)
S_1AU = 1361.0              # W m^-2, irradiância solar total a 1 UA (NASA GSFC, Solar Irradiance Science)
DAY_S = 86400.0
YEAR_DAYS = 365.25
T_SPACE_K = 3.0             # K, fundo radiativo efetivo do espaço profundo (aproximação)
ATM_KPA = 101.325
PSI_PER_KPA = 0.1450377
M_O2 = 0.031998             # kg/mol
CV_AIR = 20.8               # J mol^-1 K^-1, ar diatômico a volume constante (~5/2 R)

# Consumo metabólico nominal de O₂ por tripulante (NASA BVAD, valor nominal ~0,82 kg/dia).
O2_KG_PER_CREW_DAY = 0.82

# Semieixos maiores (UA) das órbitas planetárias, elementos keplerianos aproximados J2000
# (JPL, "Approximate Positions of the Planets", tabela 1, Standish).
PLANET_AU = {"Vênus": 0.72333566, "Terra": 1.00000261, "Marte": 1.52371034}

# Gravidade de superfície em g terrestres (NASA Planetary Fact Sheet).
SURFACE_G = {"Lua": 0.1654, "Marte": 0.3794, "Terra": 1.0}

# Aceleração residual típica dentro de uma nave em queda livre: ordem de 1 µg.
MICROGRAVITY_G = 1e-6
