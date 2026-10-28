package seed

// Name pools. Turkish names keep their diacritics on purpose, and a few
// compound names exist so worst-case {{first_name}} length checks are exercised.
var (
	trFirstNames = []string{
		"Ayşe", "Fatma", "Emine", "Hatice", "Zeynep", "Elif", "Merve", "Büşra",
		"Esra", "Özge", "Gül", "Şeyma", "Ceren", "Derya", "İrem", "Ebru",
		"Ayşe Nur", "Fatma Gül",
		"Mehmet", "Mustafa", "Ahmet", "Ali", "Hüseyin", "Hasan", "İbrahim", "Emre",
		"Burak", "Can", "Oğuz", "Murat", "Serkan", "Çağrı", "Görkem", "Barış",
		"Mehmet Ali", "Muhammed Emin", "Abdurrahman",
	}
	trLastNames = []string{
		"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk",
		"Aydın", "Özdemir", "Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara",
		"Koç", "Kurt", "Özkan", "Şimşek", "Polat", "Korkmaz", "Erdoğan", "Güneş",
		"Karaca", "Aksoy", "Bulut", "Tekin", "Uçar", "Avcı",
	}
	enFirstNames = []string{
		"James", "Olivia", "Emma", "Liam", "Noah", "Sophia", "Mia", "Lucas",
		"Amelia", "Ethan", "Harper", "Daniel", "Grace", "Henry", "Chloe", "Jack",
		"Alexander", "Charlotte", "Elizabeth",
	}
	enLastNames = []string{
		"Smith", "Johnson", "Brown", "Taylor", "Wilson", "Davies", "Evans",
		"Thomas", "Roberts", "Walker", "Wright", "Thompson", "White", "Hughes",
		"Clarke", "Hall",
	}
)

type weighted struct {
	value  string
	weight int
}

// Cities with rough population-like weights (sum 100). Istanbul dominates so
// the example brief targets a meaningful audience.
var cities = []weighted{
	{"İstanbul", 35}, {"Ankara", 12}, {"İzmir", 10}, {"Bursa", 6},
	{"Antalya", 6}, {"Adana", 5}, {"Konya", 5}, {"Gaziantep", 4},
	{"Kocaeli", 4}, {"Eskişehir", 3}, {"Trabzon", 3}, {"Kayseri", 3},
	{"Samsun", 2}, {"Diyarbakır", 2},
}
