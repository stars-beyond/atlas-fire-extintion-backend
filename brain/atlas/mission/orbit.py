"""Geometria heliocêntrica da missão: transferências keplerianas de dois corpos.

Idealização declarada: órbitas planetárias circulares e coplanares, transferência de Hohmann (ou elipse
com periélio escolhido), sem perturbações. É uma órbita fisicamente válida no problema de dois corpos,
não uma interpolação em linha reta. Trajetórias reais exigem efemérides (SPICE) e um propagador (Tudat/GMAT).
"""
from dataclasses import dataclass
import math

import numpy as np

from .constants import PLANET_AU, SURFACE_G, YEAR_DAYS, MICROGRAVITY_G


@dataclass(frozen=True)
class Profile:
    id: str
    name: str
    description: str
    r_start_au: float
    r_end_au: float
    destination: str | None
    surface_days: float = 0.0
    lunar: bool = False          # Terra → Lua: geometria heliocêntrica ≈ órbita da Terra
    transit_days_override: float | None = None

    @property
    def transfer(self) -> tuple[float, float, float]:
        """(a [UA], e, tempo de voo [dias]) da elipse de transferência."""
        a = (self.r_start_au + self.r_end_au) / 2
        e = abs(self.r_end_au - self.r_start_au) / (self.r_end_au + self.r_start_au)
        tof = 0.5 * YEAR_DAYS * a ** 1.5
        return a, e, tof

    @property
    def transit_days(self) -> float:
        return self.transit_days_override if self.transit_days_override else self.transfer[2]

    @property
    def duration_days(self) -> float:
        return self.transit_days + self.surface_days


PROFILES = {
    p.id: p for p in [
        Profile("terra-marte", "Terra → Marte (Hohmann)",
                "Transferência de Hohmann idealizada de 1,00 a 1,52 UA, seguida de estadia na superfície marciana.",
                PLANET_AU["Terra"], PLANET_AU["Marte"], "Marte", surface_days=30),
        Profile("terra-venus", "Terra → órbita de Vênus (Hohmann)",
                "Transferência para dentro, de 1,00 a 0,72 UA: a irradiância solar quase dobra.",
                PLANET_AU["Terra"], PLANET_AU["Vênus"], None),
        Profile("mergulho-solar", "Mergulho a 0,5 UA (hipotético)",
                "Elipse com afélio em 1 UA e periélio em 0,5 UA. Nave tripulada hipotética, para estressar o controle térmico.",
                PLANET_AU["Terra"], 0.5, None),
        Profile("terra-lua", "Terra → Lua",
                "Trânsito de 3 dias em queda livre e 27 dias na superfície lunar. A distância ao Sol fica perto de 1 UA.",
                PLANET_AU["Terra"], PLANET_AU["Terra"], "Lua", surface_days=27, lunar=True, transit_days_override=3.0),
    ]
}


def _solve_kepler(M: np.ndarray, e: float) -> np.ndarray:
    E = M.copy() if e < 0.8 else np.full_like(M, math.pi)
    for _ in range(50):
        dE = (E - e * np.sin(E) - M) / (1 - e * np.cos(E))
        E -= dE
        if np.max(np.abs(dE)) < 1e-12:
            break
    return E


def geometry(profile: Profile, t_days: np.ndarray) -> dict[str, np.ndarray]:
    """Distância ao Sol, posição 2D (UA), posição dos planetas e aceleração efetiva no instante t."""
    t = np.asarray(t_days, dtype=float)
    transit = profile.transit_days
    in_transit = t < transit
    n_earth = 2 * math.pi / YEAR_DAYS

    if profile.lunar:
        theta = n_earth * t
        r = np.full_like(t, PLANET_AU["Terra"])
        x, y = r * np.cos(theta), r * np.sin(theta)
        dest_x, dest_y = x, y
    else:
        a, e, tof = profile.transfer
        outward = profile.r_end_au > profile.r_start_au
        n = math.pi / tof                      # meia órbita em tof dias
        tt = np.minimum(t, tof)
        M = n * tt + (0.0 if outward else math.pi)
        E = _solve_kepler(M, e)
        nu = 2 * np.arctan2(math.sqrt(1 + e) * np.sin(E / 2), math.sqrt(1 - e) * np.cos(E / 2))
        r = a * (1 - e * np.cos(E))
        theta = nu - (0.0 if outward else math.pi)   # ângulo percorrido desde a partida: 0 → π
        # depois da chegada, a nave acompanha o destino na órbita circular dele
        n_dest = 2 * math.pi / (YEAR_DAYS * profile.r_end_au ** 1.5)
        after = t >= tof
        theta = np.where(after, math.pi + n_dest * (t - tof), theta)
        r = np.where(after, profile.r_end_au, r)
        x, y = r * np.cos(theta), r * np.sin(theta)
        dest_theta = math.pi + n_dest * (t - tof)
        dest_x, dest_y = profile.r_end_au * np.cos(dest_theta), profile.r_end_au * np.sin(dest_theta)

    earth_x, earth_y = np.cos(n_earth * t), np.sin(n_earth * t)
    g_surface = SURFACE_G.get(profile.destination or "", MICROGRAVITY_G)
    g_eff = np.where(in_transit, MICROGRAVITY_G, g_surface if profile.surface_days else MICROGRAVITY_G)
    phase = np.where(in_transit, "trânsito", "superfície" if profile.surface_days else "órbita")
    return {"r_au": r, "x_au": x, "y_au": y, "earth_x": earth_x, "earth_y": earth_y,
            "dest_x": dest_x, "dest_y": dest_y, "g_eff": g_eff, "phase": phase}


def orbit_track(profile: Profile, points: int = 240) -> dict:
    """Desenho completo para a interface: trajetória, órbitas planetárias e marcos."""
    t = np.linspace(0, profile.duration_days, points)
    g = geometry(profile, t)
    circles = {name: r for name, r in PLANET_AU.items()}
    if profile.r_end_au not in circles.values() and not profile.lunar:
        circles["periélio 0,5 UA"] = profile.r_end_au
    return {"t_days": t.tolist(), "x_au": g["x_au"].tolist(), "y_au": g["y_au"].tolist(),
            "orbits_au": circles, "transit_days": profile.transit_days}
