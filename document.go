package bongo

import (
	"fmt"
	"reflect"
	"time"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
)

// Model ...
type Model interface {
	// Collection

	GetCollName() string
	SetCollName(name string)

	// Indexes

	GetParsedIndex(name string) []ParsedIndex
	GetAllParsedIndex() map[string][]ParsedIndex
	GetIndex(name string) []*mgo.Index
	GetAllIndex() []*mgo.Index
}

// Document ...
type Document interface {
	GetID() bson.ObjectId
	SetID(bson.ObjectId)
	Model
}

// CascadingDocument ...
type CascadingDocument interface {
	GetCascade(*Collection) []*CascadeConfig
}

// DocumentNotFoundError ...
type DocumentNotFoundError struct{}

// DocumentModel ...
type DocumentModel struct {
	ID       bson.ObjectId `bson:"_id,omitempty" json:"_id"`
	Created  time.Time     `bson:"_created" json:"_created"`
	Modified time.Time     `bson:"_modified" json:"_modified"`

	// Model internal data
	collection string
	index      map[string][]ParsedIndex
	modelType  reflect.Type

	// Model lifecycle flags
	// We want this to default to false without any work. So this will be the opposite of isNew. We want it to be new unless set to existing
	exists      bool
	initialized bool
}

// SetIsNew satisfies the new tracker interface
func (d *DocumentModel) SetIsNew(isNew bool) {
	d.exists = !isNew
}

// IsNew to ask Is the document new
func (d *DocumentModel) IsNew() bool {
	return !d.exists
}

// GetID satisfies the document interface
func (d *DocumentModel) GetID() bson.ObjectId {
	return d.ID
}

// SetID sets the ID for the document
func (d *DocumentModel) SetID(id bson.ObjectId) {
	d.ID = id
}

// SetCreated sets the created date
func (d *DocumentModel) SetCreated(t time.Time) {
	d.Created = t
}

// GetCreated gets the created date
func (d *DocumentModel) GetCreated() time.Time {
	return d.Created
}

// SetModified sets the modified date
func (d *DocumentModel) SetModified(t time.Time) {
	d.Modified = t
}

// The Model interface implementation

// GetModified gets the modified date
func (d *DocumentModel) GetModified() time.Time {
	return d.Modified
}

// GetCollName implementation for the Model interface
func (d *DocumentModel) GetCollName() string {
	if !d.initialized {
		panic("This document model is not initialized. Call NewDocumentModel on type first")
	}

	return d.collection
}

// GetParsedIndex return the index stored with the passed field name
func (d *DocumentModel) GetParsedIndex(name string) []ParsedIndex {
	if !d.initialized {
		panic("This document model is not initialized. Call NewDocumentModel on type first")
	}

	return d.index[name]
}

// GetAllParsedIndex return all stored parsed indexes
func (d *DocumentModel) GetAllParsedIndex() map[string][]ParsedIndex {
	if !d.initialized {
		panic("This document model is not initialized. Call NewDocumentModel on type first")
	}

	return d.index
}

// GetIndex return the mgo.Index struct required to mgo.EnsureIndex method
// using the ParsedIndex information stored for passed field name.
// TODO: discard bad formatted indexes
func (d *DocumentModel) GetIndex(name string) []*mgo.Index {
	mi := []*mgo.Index{}

	if pi := d.GetParsedIndex(name); pi != nil {
		for i := range pi {
			mi = append(mi, BuildIndex(pi[i]))
		}
		return mi
	}

	return nil
}

// GetAllIndex return the mgo.Index struct required to mgo.EnsureIndex method
// using the ParsedIndex information stored in the index map of the Model.
// TODO: discard bad formatted indexes
func (d *DocumentModel) GetAllIndex() []*mgo.Index {
	mi := []*mgo.Index{}

	if mpi := d.GetAllParsedIndex(); mpi != nil {
		for _, v := range mpi {
			for i := range v {
				mi = append(mi, BuildIndex(v[i]))
			}
		}
		return mi
	}

	return nil
}

// SetCollName implementation for Model interface (why may you need to change collection name ?)
func (d *DocumentModel) SetCollName(name string) {
	if !d.initialized {
		panic("This document model is not initialized. Call NewDocumentModel on type first")
	}

	d.collection = name
}

func initializeModel(t reflect.Type, v reflect.Value) (map[string][]ParsedIndex, string) {
	var coll = ""
	var pi = make(map[string][]ParsedIndex, 0)

	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		ft := t.Field(i)
		n := "_" + ft.Name
		switch ft.Type.Kind() {
		case reflect.Map:
			f.Set(reflect.MakeMap(ft.Type))
			pi[n] = IndexScan(ft.Tag.Get("idx"))
		case reflect.Slice:
			f.Set(reflect.MakeSlice(ft.Type, 0, 0))
			pi[n] = IndexScan(ft.Tag.Get("idx"))
		case reflect.Chan:
			f.Set(reflect.MakeChan(ft.Type, 0))
			pi[n] = IndexScan(ft.Tag.Get("idx"))
		case reflect.Struct:
			if ft.Type.ConvertibleTo(reflect.TypeOf(DocumentModel{})) {
				coll = ft.Tag.Get("coll")
				pi[ft.Type.Name()] = IndexScan(ft.Tag.Get("idx"))
				break
			}
			rpi, _ := initializeModel(ft.Type, f)
			for k, v := range rpi {
				nn := n + k
				pi[nn] = v
			}
		case reflect.Ptr:
			fv := reflect.New(ft.Type.Elem())
			rpi, _ := initializeModel(ft.Type.Elem(), fv.Elem())
			for k, v := range rpi {
				nn := n + k
				pi[nn] = v
			}
			f.Set(fv)
		default:
			pi[n] = IndexScan(ft.Tag.Get("idx"))
		}
	}

	return pi, coll
}

// NewDocumentModel ...
func NewDocumentModel(d interface{}) interface{} {
	t := reflect.TypeOf(d)
	v := reflect.ValueOf(d)
	n := t.Name()

	if t.Kind() == reflect.Ptr {
		t = reflect.Indirect(reflect.ValueOf(d)).Type()
		v = reflect.ValueOf(d).Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("Only type struct can be used as document model (passed type %s is not struct)", n))
	}
	var DocumentModelIdx = -1
	for i := 0; i < v.NumField(); i++ {
		ft := t.Field(i)
		if ft.Type.ConvertibleTo(reflect.TypeOf(DocumentModel{})) {
			DocumentModelIdx = i
			break
		}
	}

	if DocumentModelIdx == -1 {
		panic(fmt.Sprintf("A document model must embed a DocumentModel type field (passed type %s does not have)", n))
	}

	var coll string
	var pi map[string][]ParsedIndex

	r := reflect.New(t)
	pi, coll = initializeModel(t, r.Elem())
	if coll == "" {
		panic(fmt.Sprintf("The document model does not have a collection name (passed type %s)", n))
	}
	df := r.Elem().Field(DocumentModelIdx)
	dm := df.Interface().(DocumentModel)

	dm.modelType = t
	dm.initialized = true
	dm.collection = coll
	dm.index = pi

	df.Set(reflect.ValueOf(dm))

	return r.Interface()
}

func (d DocumentNotFoundError) Error() string {
	return "Document not found"
}