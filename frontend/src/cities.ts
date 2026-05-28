// Known OLX city slugs with Ukrainian display names.
// OLX uses Russian-style transliteration for slugs (borispol not boryspil,
// vishnevoe not vyshneve, etc.) — annoying but that's their convention.
// Oblast-level slugs (ko, lvo, od, ...) give region-wide results biased
// to the main city; city-level slugs (bucha, irpen, ...) target exactly
// that city's listings.

export interface CityOption {
  name: string      // displayed to user
  slug: string      // OLX URL segment
  region?: string   // optional group label
}

export interface CategoryOption {
  name: string
  slug: string      // OLX URL segment
}

export const CITIES: CityOption[] = [
  // — Київська область —
  { name: "Київ (місто)",           slug: "kiev",            region: "Київська область" },
  { name: "Київ (вся область)",      slug: "ko",              region: "Київська область" },
  { name: "Буча",                   slug: "bucha",           region: "Київська область" },
  { name: "Ірпінь",                 slug: "irpen",           region: "Київська область" },
  { name: "Бровари",                slug: "brovary",         region: "Київська область" },
  { name: "Бориспіль",              slug: "borispol",        region: "Київська область" },
  { name: "Вишневе",                slug: "vishnevoe",       region: "Київська область" },
  { name: "Васильків",              slug: "vasilkov",        region: "Київська область" },
  { name: "Біла Церква",            slug: "belaya-tserkov",  region: "Київська область" },
  { name: "Боярка",                 slug: "boyarka",         region: "Київська область" },
  { name: "Фастів",                 slug: "fastov",          region: "Київська область" },
  { name: "Переяслав",              slug: "pereyaslav",      region: "Київська область" },

  // — Великі міста України —
  { name: "Львів",                  slug: "lvov",            region: "Інші міста" },
  { name: "Одеса",                  slug: "odessa",          region: "Інші міста" },
  { name: "Харків",                 slug: "kharkov",         region: "Інші міста" },
  { name: "Дніпро",                 slug: "dnepr",           region: "Інші міста" },
  { name: "Запоріжжя",              slug: "zaporozhe",       region: "Інші міста" },
  { name: "Вінниця",                slug: "vinnitsa",        region: "Інші міста" },
  { name: "Луцьк",                  slug: "lutsk",           region: "Інші міста" },
  { name: "Полтава",                slug: "poltava",         region: "Інші міста" },
  { name: "Тернопіль",              slug: "ternopol",        region: "Інші міста" },
  { name: "Хмельницький",           slug: "khmelnytskyy",   region: "Інші міста" },
  { name: "Чернівці",               slug: "chernovtsy",      region: "Інші міста" },
  { name: "Рівне",                  slug: "rovno",           region: "Інші міста" },
  { name: "Івано-Франківськ",       slug: "ivano-frankovsk", region: "Інші міста" },
  { name: "Ужгород",                slug: "uzhgorod",        region: "Інші міста" },
  { name: "Чернігів",               slug: "chernigov",       region: "Інші міста" },
  { name: "Суми",                   slug: "sumy",            region: "Інші міста" },
  { name: "Житомир",                slug: "zhitomir",        region: "Інші міста" },
  { name: "Кропивницький",          slug: "kropivnitskiy",   region: "Інші міста" },
  { name: "Запоріжжя",              slug: "zaporozhe",       region: "Інші міста" },
]

export const CATEGORIES: CategoryOption[] = [
  { name: "Продаж квартир",          slug: "prodazha-kvartir" },
  { name: "Продаж будинків",         slug: "prodazha-domov" },
  { name: "Довгострокова оренда квартир", slug: "arenda-kvartir" },
  { name: "Земельні ділянки",        slug: "zemlya" },
  { name: "Комерційна нерухомість",  slug: "prodazha-pomescheniy" },
]