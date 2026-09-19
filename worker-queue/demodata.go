package main

// Demo-only workers and facilities for the hackathon.
// This is NOT production authentication. Passwords are fixed demo values only.

type DemoWorker struct {
	WorkerID     string
	Username     string
	Password     string
	District     string
	FacilityID   string
	FacilityName string
}

type DemoFacility struct {
	FacilityID        string
	Name              string
	District          string
	Lat               float64
	Long              float64
	StatusOpen        bool
	StatusHasDoctor   bool
	StatusHasMedicine bool
	LastUpdatedBy     string
}

// DemoWorkers maps one demo worker to one facility per supported district.
var DemoWorkers = []DemoWorker{
	{WorkerID: "worker-warangal-1", Username: "warangal.worker", Password: "demo123", District: "Warangal", FacilityID: "WARANGAL-001", FacilityName: "Warangal Demo Community Health Centre"},
	{WorkerID: "worker-hanamkonda-1", Username: "hanamkonda.worker", Password: "demo123", District: "Hanamkonda", FacilityID: "HANAMKONDA-001", FacilityName: "Hanamkonda Demo Primary Health Centre"},
	{WorkerID: "worker-karimnagar-1", Username: "karimnagar.worker", Password: "demo123", District: "Karimnagar", FacilityID: "KARIMNAGAR-001", FacilityName: "Karimnagar Demo Rural Clinic"},
	{WorkerID: "worker-khammam-1", Username: "khammam.worker", Password: "demo123", District: "Khammam", FacilityID: "KHAMMAM-001", FacilityName: "Khammam Demo Community Health Centre"},
	{WorkerID: "worker-nalgonda-1", Username: "nalgonda.worker", Password: "demo123", District: "Nalgonda", FacilityID: "NALGONDA-001", FacilityName: "Nalgonda Demo Primary Health Centre"},
	{WorkerID: "worker-nizamabad-1", Username: "nizamabad.worker", Password: "demo123", District: "Nizamabad", FacilityID: "NIZAMABAD-001", FacilityName: "Nizamabad Demo Rural Clinic"},
	{WorkerID: "worker-adilabad-1", Username: "adilabad.worker", Password: "demo123", District: "Adilabad", FacilityID: "ADILABAD-001", FacilityName: "Adilabad Demo Community Health Centre"},
	{WorkerID: "worker-mahabubnagar-1", Username: "mahabubnagar.worker", Password: "demo123", District: "Mahabubnagar", FacilityID: "MAHABUBNAGAR-001", FacilityName: "Mahabubnagar Demo Primary Health Centre"},
}

// DemoFacilities are fictional demo sites for seeding the Facilities table.
var DemoFacilities = []DemoFacility{
	{FacilityID: "WARANGAL-001", Name: "Warangal Demo Community Health Centre", District: "Warangal", Lat: 17.9689, Long: 79.5941, StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	{FacilityID: "HANAMKONDA-001", Name: "Hanamkonda Demo Primary Health Centre", District: "Hanamkonda", Lat: 18.0079, Long: 79.5587, StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	{FacilityID: "KARIMNAGAR-001", Name: "Karimnagar Demo Rural Clinic", District: "Karimnagar", Lat: 18.4386, Long: 79.1288, StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	{FacilityID: "KHAMMAM-001", Name: "Khammam Demo Community Health Centre", District: "Khammam", Lat: 17.2473, Long: 80.1514, StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	{FacilityID: "NALGONDA-001", Name: "Nalgonda Demo Primary Health Centre", District: "Nalgonda", Lat: 17.0575, Long: 79.2684, StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	{FacilityID: "NIZAMABAD-001", Name: "Nizamabad Demo Rural Clinic", District: "Nizamabad", Lat: 18.6725, Long: 78.0941, StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	{FacilityID: "ADILABAD-001", Name: "Adilabad Demo Community Health Centre", District: "Adilabad", Lat: 19.6641, Long: 78.5320, StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	{FacilityID: "MAHABUBNAGAR-001", Name: "Mahabubnagar Demo Primary Health Centre", District: "Mahabubnagar", Lat: 16.7488, Long: 78.0039, StatusOpen: true, StatusHasDoctor: true, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	// Extra closed/unstaffed facilities so lookup filtering can be demonstrated after status changes.
	{FacilityID: "WARANGAL-002", Name: "Warangal Demo Night Dispensary (closed seed)", District: "Warangal", Lat: 17.9500, Long: 79.5800, StatusOpen: false, StatusHasDoctor: false, StatusHasMedicine: true, LastUpdatedBy: "seed"},
	{FacilityID: "KHAMMAM-002", Name: "Khammam Demo Outreach Post (no doctor seed)", District: "Khammam", Lat: 17.2600, Long: 80.1400, StatusOpen: true, StatusHasDoctor: false, StatusHasMedicine: false, LastUpdatedBy: "seed"},
}

func FindWorkerByUsername(username string) (DemoWorker, bool) {
	for _, w := range DemoWorkers {
		if w.Username == username {
			return w, true
		}
	}
	return DemoWorker{}, false
}

func FindWorkerByID(workerID string) (DemoWorker, bool) {
	for _, w := range DemoWorkers {
		if w.WorkerID == workerID {
			return w, true
		}
	}
	return DemoWorker{}, false
}

func AuthenticateDemoWorker(username, password string) (DemoWorker, bool) {
	w, ok := FindWorkerByUsername(username)
	if !ok || w.Password != password {
		return DemoWorker{}, false
	}
	return w, true
}
